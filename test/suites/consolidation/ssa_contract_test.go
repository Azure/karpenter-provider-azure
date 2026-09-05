/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package consolidation_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// This is an independent Kubernetes protocol test, not a fake-client test of SSA
// and not a live-cluster rollout test. It deliberately does not call the fixture's
// placement implementation: the API contract must hold before that code relies on it.
func TestReplacementBudgetSSAContract(t *testing.T) {
	deployments := newReplacementBudgetSSAAPI(t)
	tests := []struct {
		name string
		run  func(*testing.T, appsv1client.DeploymentInterface)
	}{
		{name: "owner reapply and precise rollback", run: testReplacementBudgetSSAOwnerReapply},
		{name: "identity and version fences", run: testReplacementBudgetSSAFences},
		{name: "conflicting selector is not stolen", run: testReplacementBudgetSSAConflict},
		{name: "coownership preserves another manager", run: testReplacementBudgetSSACoownership},
		{name: "external selector change is preserved", run: testReplacementBudgetSSAExternalChange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, deployments) })
	}
}

func newReplacementBudgetSSAAPI(t *testing.T) appsv1client.DeploymentInterface {
	t.Helper()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		assets = "/usr/local/kubebuilder/bin"
	}
	for _, binary := range []string{"kube-apiserver", "etcd", "kubectl"} {
		path := filepath.Join(assets, binary)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("SSA contract requires installed envtest asset %s: %v", path, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			t.Fatalf("SSA contract envtest asset is not executable: %s", path)
		}
	}
	// Pin the verified asset paths and explicitly refuse any existing kubeconfig,
	// including when USE_EXISTING_CLUSTER or TEST_ASSET_* is set in the caller.
	apiServer := &envtest.Environment{
		UseExistingCluster:   ptr.To(false),
		DownloadBinaryAssets: false,
		ControlPlane: envtest.ControlPlane{
			APIServer:   &envtest.APIServer{Path: filepath.Join(assets, "kube-apiserver")},
			Etcd:        &envtest.Etcd{Path: filepath.Join(assets, "etcd")},
			KubectlPath: filepath.Join(assets, "kubectl"),
		},
		ControlPlaneStartTimeout: time.Minute,
		ControlPlaneStopTimeout:  time.Minute,
	}
	t.Cleanup(func() {
		if err := apiServer.Stop(); err != nil {
			t.Errorf("stop SSA contract API server: %v", err)
		}
	})
	g := NewWithT(t)
	config, err := apiServer.Start()
	g.Expect(err).ToNot(HaveOccurred(), "start a disposable API server, never use a live cluster")
	kubeClient, err := kubernetes.NewForConfig(config)
	g.Expect(err).ToNot(HaveOccurred())
	version, err := kubeClient.Discovery().ServerVersion()
	g.Expect(err).ToNot(HaveOccurred())
	t.Logf("SSA contract uses real Kubernetes %s with installed envtest assets", version.GitVersion)
	return kubeClient.AppsV1().Deployments("default")
}

func testReplacementBudgetSSAOwnerReapply(t *testing.T, deployments appsv1client.DeploymentInterface) {
	g := NewWithT(t)
	ctx := ssaContractContext(t)
	owner := ssaContractOwner()
	ownerOptions := metav1.PatchOptions{FieldManager: "deployment-owner", Force: ptr.To(true)}
	fixtureOptions := metav1.PatchOptions{FieldManager: "consolidation-fixture-" + uuid.NewString()}
	baseline, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(baseline.Spec.Template.Spec.NodeSelector).To(BeEmpty())
	g.Expect(ssaContractFieldOwners(t, baseline, "f:spec", "f:template", "f:spec", "f:nodeSelector")).To(BeEmpty())
	g.Expect(ssaContractFieldOwners(t, baseline, "f:spec", "f:template", "f:spec", "f:affinity")).To(Equal([]string{ownerOptions.FieldManager}))

	current := ssaContractGet(t, ctx, deployments, baseline.Name)
	installed, err := ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, true), fixtureOptions)
	g.Expect(err).ToNot(HaveOccurred())
	assertSSAContractSelector(t, baseline, installed, fixtureOptions.FieldManager)

	// Force applies only to the other owner's declared fields, not to a separately
	// owned sibling field omitted from that owner's original intent.
	reapplied, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	assertSSAContractSelector(t, baseline, reapplied, fixtureOptions.FieldManager)

	changedOwner := owner.DeepCopy()
	changedOwner.Spec.Template.Spec.Containers[0].Image = "registry.k8s.io/pause:3.9"
	changedOwner.Annotations["example.com/revision"] = "updated"
	changedOwner.Spec.Template.Annotations["example.com/revision"] = "updated"
	dryRunOptions := ownerOptions
	dryRunOptions.DryRun = []string{metav1.DryRunAll}
	beforeDryRun := ssaContractGet(t, ctx, deployments, owner.Name)
	preview, err := ssaContractApply(t, ctx, deployments, owner.Name, changedOwner, dryRunOptions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(preview.Spec.Template.Spec.Containers[0].Image).To(Equal(changedOwner.Spec.Template.Spec.Containers[0].Image))
	g.Expect(preview.Annotations).To(Equal(changedOwner.Annotations))
	g.Expect(preview.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(v1beta1.AKSLabelMode, v1beta1.ModeSystem))
	g.Expect(ssaContractFieldOwners(t, preview, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(Equal([]string{fixtureOptions.FieldManager}))
	g.Expect(ssaContractGet(t, ctx, deployments, owner.Name)).To(Equal(beforeDryRun), "dry-run changes must not persist")

	reapplied, err = ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	assertSSAContractSelector(t, baseline, reapplied, fixtureOptions.FieldManager)
	updated, err := ssaContractApply(t, ctx, deployments, owner.Name, changedOwner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(v1beta1.AKSLabelMode, v1beta1.ModeSystem))
	g.Expect(updated.Spec.Template.Spec.Containers[0].Image).To(Equal(changedOwner.Spec.Template.Spec.Containers[0].Image))
	g.Expect(updated.Annotations).To(Equal(changedOwner.Annotations))
	g.Expect(updated.Spec.Template.Annotations).To(Equal(changedOwner.Spec.Template.Annotations))
	g.Expect(ssaContractFieldOwners(t, updated, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(Equal([]string{fixtureOptions.FieldManager}))

	current = ssaContractGet(t, ctx, deployments, owner.Name)
	// Omit only this manager's intent. Never restore the old Deployment preimage,
	// which would overwrite the image and annotations changed by the other owner.
	restored, err := ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, false), fixtureOptions)
	g.Expect(err).ToNot(HaveOccurred())
	expected := current.DeepCopy()
	expected.Spec.Template.Spec.NodeSelector = nil
	g.Expect(restored.UID).To(Equal(baseline.UID))
	g.Expect(restored.Spec).To(Equal(expected.Spec))
	g.Expect(restored.Labels).To(Equal(current.Labels))
	g.Expect(restored.Annotations).To(Equal(changedOwner.Annotations))
	g.Expect(ssaContractFieldOwners(t, restored, "f:spec", "f:template", "f:spec", "f:nodeSelector")).To(BeEmpty())
	g.Expect(ssaContractGet(t, ctx, deployments, owner.Name)).To(Equal(restored))
}

func testReplacementBudgetSSAFences(t *testing.T, deployments appsv1client.DeploymentInterface) {
	for _, operation := range []string{"activation", "rollback"} {
		t.Run(operation, func(t *testing.T) {
			for _, fence := range []string{"missing object", "replacement UID", "stale resourceVersion"} {
				t.Run(fence, func(t *testing.T) {
					assertSSAContractFence(t, deployments, operation == "rollback", fence)
				})
			}
		})
	}
}

func assertSSAContractFence(t *testing.T, deployments appsv1client.DeploymentInterface, rollback bool, fence string) {
	t.Helper()
	g := NewWithT(t)
	ctx := ssaContractContext(t)
	owner := ssaContractOwner()
	ownerOptions := metav1.PatchOptions{FieldManager: "deployment-owner", Force: ptr.To(true)}
	fixtureOptions := metav1.PatchOptions{FieldManager: "consolidation-fixture-" + uuid.NewString()}
	current, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	if rollback {
		current, err = ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, true), fixtureOptions)
		g.Expect(err).ToNot(HaveOccurred())
	}
	identity := current.DeepCopy()
	switch fence {
	case "missing object", "replacement UID":
		g.Expect(deployments.Delete(ctx, current.Name, metav1.DeleteOptions{
			Preconditions:     &metav1.Preconditions{UID: &current.UID},
			PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
		})).To(Succeed())
		_, err = deployments.Get(ctx, current.Name, metav1.GetOptions{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "deleted Deployment must actually be absent: %v", err)
		if fence == "replacement UID" {
			current, err = ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(current.UID).ToNot(Equal(identity.UID))
			// Use the replacement's fresh RV so a rejection proves the UID fence,
			// not merely a stale resourceVersion from the deleted object.
			identity.ResourceVersion = current.ResourceVersion
		}
	case "stale resourceVersion":
		changedOwner := owner.DeepCopy()
		changedOwner.Annotations["example.com/revision"] = "intervening-update"
		current, err = ssaContractApply(t, ctx, deployments, owner.Name, changedOwner, ownerOptions)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(current.UID).To(Equal(identity.UID))
		g.Expect(current.ResourceVersion).ToNot(Equal(identity.ResourceVersion))
	default:
		t.Fatalf("unknown fence %q", fence)
	}

	_, err = ssaContractApply(t, ctx, deployments, identity.Name, ssaContractSelectorIntent(identity, !rollback), fixtureOptions)
	g.Expect(err).To(HaveOccurred(), "identity/version fencing must reject the stale intent")
	if fence == "missing object" {
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "%v", err)
		g.Expect(err.Error()).To(ContainSubstring("uid mismatch"), "rejection must exercise UID fencing, not incomplete Deployment validation")
		_, getErr := deployments.Get(ctx, identity.Name, metav1.GetOptions{})
		g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "SSA must not upsert the deleted Deployment: %v", getErr)
		return
	}
	if fence == "replacement UID" {
		g.Expect(apierrors.IsInvalid(err) || apierrors.IsConflict(err)).To(BeTrue(), "%v", err)
		g.Expect(err.Error()).To(ContainSubstring("uid"))
	} else {
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "%v", err)
	}
	g.Expect(ssaContractGet(t, ctx, deployments, owner.Name)).To(Equal(current), "a rejected intent must not modify the persisted object")
}

func testReplacementBudgetSSAConflict(t *testing.T, deployments appsv1client.DeploymentInterface) {
	g := NewWithT(t)
	ctx := ssaContractContext(t)
	owner := ssaContractOwner()
	owner.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeUser}
	current, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, metav1.PatchOptions{FieldManager: "deployment-owner"})
	g.Expect(err).ToNot(HaveOccurred())
	_, err = ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, true), metav1.PatchOptions{
		FieldManager: "consolidation-fixture-" + uuid.NewString(),
	})
	g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "the fixture must not force an already owned selector: %v", err)
	g.Expect(err.Error()).To(ContainSubstring("deployment-owner"))
	g.Expect(err.Error()).To(ContainSubstring(v1beta1.AKSLabelMode))
	g.Expect(ssaContractGet(t, ctx, deployments, owner.Name)).To(Equal(current))
}

func testReplacementBudgetSSACoownership(t *testing.T, deployments appsv1client.DeploymentInterface) {
	for _, preexisting := range []bool{false, true} {
		t.Run(map[bool]string{false: "adopted after activation", true: "already owned same value"}[preexisting], func(t *testing.T) {
			g := NewWithT(t)
			ctx := ssaContractContext(t)
			owner := ssaContractOwner()
			if preexisting {
				owner.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
			}
			ownerOptions := metav1.PatchOptions{FieldManager: "deployment-owner"}
			fixtureOptions := metav1.PatchOptions{FieldManager: "consolidation-fixture-" + uuid.NewString()}
			current, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
			g.Expect(err).ToNot(HaveOccurred())
			_, err = ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, true), fixtureOptions)
			g.Expect(err).ToNot(HaveOccurred(), "same-value SSA can share ownership even without force")
			owner.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
			shared, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ssaContractFieldOwners(t, shared, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(ConsistOf(fixtureOptions.FieldManager, ownerOptions.FieldManager))

			current = ssaContractGet(t, ctx, deployments, owner.Name)
			released, err := ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, false), fixtureOptions)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(released.Spec).To(Equal(current.Spec), "omission cannot remove a field another manager still owns")
			g.Expect(released.Annotations).To(Equal(current.Annotations))
			g.Expect(ssaContractFieldOwners(t, released, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(Equal([]string{ownerOptions.FieldManager}))
			// A successful omission is not proof of restored placement. The fixture
			// must reject a pre-owned selector and detect subsequent coownership.
			g.Expect(released.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(v1beta1.AKSLabelMode, v1beta1.ModeSystem))
		})
	}
}

func testReplacementBudgetSSAExternalChange(t *testing.T, deployments appsv1client.DeploymentInterface) {
	g := NewWithT(t)
	ctx := ssaContractContext(t)
	owner := ssaContractOwner()
	ownerOptions := metav1.PatchOptions{FieldManager: "deployment-owner", Force: ptr.To(true)}
	fixtureOptions := metav1.PatchOptions{FieldManager: "consolidation-fixture-" + uuid.NewString()}
	current, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	_, err = ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, true), fixtureOptions)
	g.Expect(err).ToNot(HaveOccurred())
	owner.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeUser}
	changed, err := ssaContractApply(t, ctx, deployments, owner.Name, owner, ownerOptions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ssaContractFieldOwners(t, changed, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(Equal([]string{ownerOptions.FieldManager}))
	current = ssaContractGet(t, ctx, deployments, owner.Name)
	released, err := ssaContractApply(t, ctx, deployments, current.Name, ssaContractSelectorIntent(current, false), fixtureOptions)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(released.Spec).To(Equal(current.Spec))
	g.Expect(released.UID).To(Equal(current.UID))
	g.Expect(released.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(v1beta1.AKSLabelMode, v1beta1.ModeUser))
	g.Expect(ssaContractFieldOwners(t, released, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(Equal([]string{ownerOptions.FieldManager}))
}

func ssaContractOwner() *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "ssa-contract-" + uuid.NewString(), Namespace: "default",
			Labels: map[string]string{"app": "ssa-contract"}, Annotations: map[string]string{"example.com/revision": "original"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](2),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "ssa-contract"}},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxSurge: ptr.To(intstr.FromInt32(1)), MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "ssa-contract"}, Annotations: map[string]string{"example.com/revision": "original"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "workload", Image: "registry.k8s.io/pause:3.10"}},
					Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
							Weight: 100,
							Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
								Key: v1beta1.AKSLabelMode, Operator: corev1.NodeSelectorOpIn, Values: []string{v1beta1.ModeSystem},
							}}},
						}},
					}},
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
						{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
					},
				},
			},
		},
	}
}

func ssaContractSelectorIntent(current *appsv1.Deployment, includeSelector bool) map[string]interface{} {
	// Use an explicit partial intent, not a marshaled Deployment with zero-valued
	// fields. Omitting spec on release relinquishes only this manager's field set.
	intent := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name": current.Name, "namespace": current.Namespace,
			"uid": current.UID, "resourceVersion": current.ResourceVersion,
		},
	}
	if includeSelector {
		intent["spec"] = map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"nodeSelector": map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem},
		}}}
	}
	return intent
}

func ssaContractApply(t *testing.T, ctx context.Context, deployments appsv1client.DeploymentInterface, name string, intent interface{}, options metav1.PatchOptions) (*appsv1.Deployment, error) {
	t.Helper()
	data, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal SSA contract intent: %v", err)
	}
	return deployments.Patch(ctx, name, types.ApplyPatchType, data, options)
}

func ssaContractGet(t *testing.T, ctx context.Context, deployments appsv1client.DeploymentInterface, name string) *appsv1.Deployment {
	t.Helper()
	current, err := deployments.Get(ctx, name, metav1.GetOptions{})
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return current
}

func ssaContractContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func assertSSAContractSelector(t *testing.T, baseline, current *appsv1.Deployment, manager string) {
	t.Helper()
	g := NewWithT(t)
	g.Expect(current.UID).To(Equal(baseline.UID))
	g.Expect(current.Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}))
	withoutSelector := current.DeepCopy()
	withoutSelector.Spec.Template.Spec.NodeSelector = baseline.Spec.Template.Spec.NodeSelector
	g.Expect(withoutSelector.Spec).To(Equal(baseline.Spec), "selector ownership must preserve all other spec fields")
	g.Expect(current.Labels).To(Equal(baseline.Labels))
	g.Expect(current.Annotations).To(Equal(baseline.Annotations))
	g.Expect(current.OwnerReferences).To(Equal(baseline.OwnerReferences))
	g.Expect(current.Finalizers).To(Equal(baseline.Finalizers))
	g.Expect(current.Labels).ToNot(HaveKey(coretest.DiscoveryLabel))
	g.Expect(current.Spec.Template.Labels).ToNot(HaveKey(coretest.DiscoveryLabel))
	g.Expect(ssaContractFieldOwners(t, current, "f:spec", "f:template", "f:spec", "f:nodeSelector", "f:"+v1beta1.AKSLabelMode)).To(Equal([]string{manager}))
	g.Expect(ssaContractFieldOwners(t, current, "f:spec", "f:template", "f:spec", "f:affinity")).To(Equal([]string{"deployment-owner"}))
}

func ssaContractFieldOwners(t *testing.T, deployment *appsv1.Deployment, path ...string) []string {
	t.Helper()
	var owners []string
	for _, entry := range deployment.ManagedFields {
		if entry.Subresource != "" || entry.FieldsV1 == nil {
			continue
		}
		var fields map[string]interface{}
		if err := json.Unmarshal(entry.FieldsV1.Raw, &fields); err != nil {
			t.Fatalf("decode API-server managedFields for %s: %v", entry.Manager, err)
		}
		found := true
		for _, field := range path {
			nested, ok := fields[field].(map[string]interface{})
			if !ok {
				found = false
				break
			}
			fields = nested
		}
		if found {
			NewWithT(t).Expect(entry.Operation).To(Equal(metav1.ManagedFieldsOperationApply))
			owners = append(owners, entry.Manager)
		}
	}
	sort.Strings(owners)
	return owners
}
