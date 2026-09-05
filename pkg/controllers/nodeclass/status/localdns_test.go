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

package status

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

const (
	hiK8s = "1.36.0"
	loK8s = "1.35.0"

	smallSKU = "Standard_B2s"    // 2 vCPU -- below the LocalDNS floor
	largeSKU = "Standard_D4s_v3" // 4 vCPU -- above the LocalDNS floor
)

// stubInstanceTypeProvider stands in for the real instance type provider. It
// mirrors the one behavior this reconciler depends on: List returns the
// LocalDNS-filtered set only when the NodeClass it is handed reports LocalDNS
// as enabled. That makes the probe in nodePoolsWithoutCompatibleInstanceTypes
// observable -- a reconciler that forgot to set LocalDNSState on the probe
// would see the unfiltered set and wrongly pass the gate.
type stubInstanceTypeProvider struct {
	all               []*corecloudprovider.InstanceType
	localDNSSupported []*corecloudprovider.InstanceType
	err               error
}

var _ instancetype.Provider = (*stubInstanceTypeProvider)(nil)

func (s *stubInstanceTypeProvider) List(_ context.Context, nc *v1beta1.AKSNodeClass) ([]*corecloudprovider.InstanceType, error) {
	if s.err != nil {
		return nil, s.err
	}
	if nc.IsLocalDNSEnabled() {
		return s.localDNSSupported, nil
	}
	return s.all, nil
}

func (s *stubInstanceTypeProvider) Get(_ context.Context, _ string) (*skewer.SKU, error) {
	return nil, errors.New("not implemented")
}
func (s *stubInstanceTypeProvider) UpdateInstanceTypes(_ context.Context) error { return nil }
func (s *stubInstanceTypeProvider) LivenessProbe(_ *http.Request) error         { return nil }

// newInstanceType builds an instance type carrying the two labels a NodePool
// realistically selects on when pinning VM size.
func newInstanceType(name string, cpu int) *corecloudprovider.InstanceType {
	return &corecloudprovider.InstanceType{
		Name: name,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, name),
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, fmt.Sprint(cpu)),
		),
	}
}

// newNodePool builds a NodePool referencing nodeClassName, optionally pinned to
// a set of instance types.
func newNodePool(name, nodeClassName string, instanceTypes ...string) *karpv1.NodePool {
	np := &karpv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: karpv1.NodePoolSpec{
			Template: karpv1.NodeClaimTemplate{
				Spec: karpv1.NodeClaimTemplateSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: "karpenter.azure.com",
						Kind:  "AKSNodeClass",
						Name:  nodeClassName,
					},
				},
			},
		},
	}
	if len(instanceTypes) > 0 {
		np.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{{
			Key:      corev1.LabelInstanceTypeStable,
			Operator: corev1.NodeSelectorOpIn,
			Values:   instanceTypes,
		}}
	}
	return np
}

func newCRClient(nodePools ...*karpv1.NodePool) client.Client {
	b := crfake.NewClientBuilder()
	for _, np := range nodePools {
		b = b.WithObjects(np)
	}
	return b.Build()
}

// defaultInstanceTypeProvider offers one small and one large SKU, with only the
// large one surviving the LocalDNS filter.
func defaultInstanceTypeProvider() *stubInstanceTypeProvider {
	small, large := newInstanceType(smallSKU, 2), newInstanceType(largeSKU, 4)
	return &stubInstanceTypeProvider{
		all:               []*corecloudprovider.InstanceType{small, large},
		localDNSSupported: []*corecloudprovider.InstanceType{large},
	}
}

// newReconciler builds a LocalDNSReconciler whose instance-type gate passes, so
// the pre-existing tests keep exercising only the gate they care about.
func newReconciler(kubeClient kubernetes.Interface, dynamicClient dynamic.Interface, networkPolicy, networkPlugin string) *LocalDNSReconciler {
	return NewLocalDNSReconciler(
		kubeClient,
		dynamicClient,
		newCRClient(newNodePool("default", "test")),
		defaultInstanceTypeProvider(),
		networkPolicy,
		networkPlugin,
	)
}

func newDynFake() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:             "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}:  "CiliumClusterwideNetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:       "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	})
}

func newNC() *v1beta1.AKSNodeClass {
	nc := &v1beta1.AKSNodeClass{}
	nc.Name = "test"
	nc.Generation = 1
	return nc
}

func setKVReady(nc *v1beta1.AKSNodeClass, k8sVer string) {
	nc.Status.KubernetesVersion = lo.ToPtr(k8sVer)
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
}

func mustReconcile(t *testing.T, r *LocalDNSReconciler, nc *v1beta1.AKSNodeClass) {
	t.Helper()
	g := NewWithT(t)
	_, err := r.Reconcile(context.Background(), nc)
	g.Expect(err).ToNot(HaveOccurred())
}

func expectState(t *testing.T, nc *v1beta1.AKSNodeClass, want v1beta1.LocalDNSState) {
	t.Helper()
	g := NewWithT(t)
	g.Expect(nc.Status.LocalDNSState).ToNot(BeNil(), "expected LocalDNSState=%q, got nil", want)
	g.Expect(*nc.Status.LocalDNSState).To(Equal(want))
}

func TestModeUnsetSetsDisabled(t *testing.T) {
	nc := newNC()
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled) // stale
	r := newReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	if !nc.StatusConditions().IsTrue(v1beta1.ConditionTypeLocalDNSReady) {
		t.Fatalf("expected LocalDNSReady=True")
	}
}

func TestModeRequired(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}
	r := newReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestModeDisabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
	r := newReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_K8sBelowThreshold_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, loK8s)
	r := newReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_BYOCNI_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	r := newReconciler(fake.NewClientset(), newDynFake(), "", consts.NetworkPluginNone)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_Ubuntu2004_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	nc.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
	nc.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
	setKVReady(nc, hiK8s)
	r := newReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_NoConflicts_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	r := newReconciler(fake.NewClientset(), newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_NodeLocalDNSPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset(&appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-local-dns", Namespace: "kube-system"},
	})
	r := newReconciler(k8sFake, newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_NetworkPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	r := newReconciler(k8sFake, newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_CiliumClusterwidePolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:            "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}: "CiliumClusterwideNetworkPolicyList",
	},
		unstructuredObj("cilium.io/v2", "CiliumClusterwideNetworkPolicy", "", "deny-cluster"),
	)
	r := newReconciler(fake.NewClientset(), dc, "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_CalicoNamespacedPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:       "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	},
		unstructuredObj("crd.projectcalico.org/v1", "NetworkPolicy", "default", "deny-ns"),
	)
	r := newReconciler(fake.NewClientset(), dc, "calico", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

// TestPreferred_NPM_K8sNetworkPolicyPresent_Enabled asserts that when the
// cluster's network policy mode is not Cilium/Calico (e.g. Azure NPM, or
// empty), built-in K8s NetworkPolicies are NOT consulted and a conflicting
// policy does not flip Preferred to Disabled.
func TestPreferred_NPM_K8sNetworkPolicyPresent_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	// networkPolicy="" simulates NPM / no recognized CRD-based engine.
	r := newReconciler(k8sFake, newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_KonnectivityAgentIgnored(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "konnectivity-agent", Namespace: "kube-system"},
	})
	r := newReconciler(k8sFake, newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_StickyEnabled_DoesNotFlipOnNewConflict(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	k8sFake := fake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	r := newReconciler(k8sFake, newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_TransientError_RequeuesViaError(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset()
	k8sFake.PrependReactor("list", "networkpolicies", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("transient")
	})
	r := newReconciler(k8sFake, newDynFake(), "cilium", "azure")
	_, err := r.Reconcile(context.Background(), nc)
	if err == nil {
		t.Fatalf("expected error on transient failure")
	}
	if nc.Status.LocalDNSState != nil {
		t.Fatalf("state should not be committed on transient error, got %v", *nc.Status.LocalDNSState)
	}
	if nc.StatusConditions().IsTrue(v1beta1.ConditionTypeLocalDNSReady) {
		t.Fatalf("LocalDNSReady should not be True on transient error")
	}
}

func TestPreferred_DaemonSetGetError_Requeues(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset()
	k8sFake.PrependReactor("get", "daemonsets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("rbac forbidden")
	})
	r := newReconciler(k8sFake, newDynFake(), "", "azure")
	_, err := r.Reconcile(context.Background(), nc)
	if err == nil {
		t.Fatalf("expected error on DS get failure")
	}
}

func TestPreferred_DaemonSetGetNotFound_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	k8sFake := fake.NewClientset()
	k8sFake.PrependReactor("get", "daemonsets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "node-local-dns")
	})
	r := newReconciler(k8sFake, newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_CiliumCRDPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:            "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}: "CiliumClusterwideNetworkPolicyList",
	},
		unstructuredObj("cilium.io/v2", "CiliumNetworkPolicy", "default", "deny"),
	)
	r := newReconciler(fake.NewClientset(), dc, "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_CalicoCRDPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:       "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	},
		unstructuredObj("crd.projectcalico.org/v1", "GlobalNetworkPolicy", "", "deny-all"),
	)
	r := newReconciler(fake.NewClientset(), dc, "calico", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

// TestPreferred_CiliumCRDNotInstalled_Enabled covers the case where the
// Cilium CRDs are not registered in the dynamic client at all (cluster does
// not have Cilium CRDs installed). The gate must treat this as "no
// conflicting policies" and let LocalDNS resolve to Enabled, not surface the
// discovery error and flip to Disabled.
func TestPreferred_CiliumCRDNotInstalled_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	dc := newDynFake()
	dc.PrependReactor("list", "ciliumnetworkpolicies", noKindMatchReactor("cilium.io", "CiliumNetworkPolicy"))
	dc.PrependReactor("list", "ciliumclusterwidenetworkpolicies", noKindMatchReactor("cilium.io", "CiliumClusterwideNetworkPolicy"))
	r := newReconciler(fake.NewClientset(), dc, "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// TestPreferred_CalicoCRDNotInstalled_Enabled is the Calico counterpart of
// TestPreferred_CiliumCRDNotInstalled_Enabled.
func TestPreferred_CalicoCRDNotInstalled_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	dc := newDynFake()
	dc.PrependReactor("list", "networkpolicies", noKindMatchReactor("crd.projectcalico.org", "NetworkPolicy"))
	dc.PrependReactor("list", "globalnetworkpolicies", noKindMatchReactor("crd.projectcalico.org", "GlobalNetworkPolicy"))
	r := newReconciler(fake.NewClientset(), dc, "calico", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// noKindMatchReactor returns a reactor that simulates the API server reporting
// that a CRD's Kind is not registered -- i.e., the CRD is not installed.
func noKindMatchReactor(group, kind string) clienttesting.ReactionFunc {
	return func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, &meta.NoKindMatchError{
			GroupKind:        schema.GroupKind{Group: group, Kind: kind},
			SearchedVersions: []string{"v1", "v2"},
		}
	}
}

// unstructuredObj builds an *unstructured.Unstructured for the fake dynamic client.
func unstructuredObj(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	if namespace != "" {
		u.SetNamespace(namespace)
	}
	u.SetName(name)
	return u
}

// --- instance type gate ---

// preferredNC builds a NodeClass in Preferred mode that clears every gate
// except the instance type one.
func preferredNC() *v1beta1.AKSNodeClass {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	return nc
}

func newLocalDNSReconcilerWith(crClient client.Client, itProvider instancetype.Provider) *LocalDNSReconciler {
	return NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), crClient, itProvider, "", "azure")
}

func expectReadyReason(t *testing.T, nc *v1beta1.AKSNodeClass, want string) {
	t.Helper()
	g := NewWithT(t)
	cond := nc.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
	g.Expect(cond).ToNot(BeNil())
	g.Expect(cond.Reason).To(Equal(want))
}

func TestPreferred_NodePoolPinnedToSmallSKU_Disabled(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("small", "test", smallSKU)),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoCompatibleInstanceTypes)
}

func TestPreferred_NodePoolPinnedToLargeSKU_Enabled(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("large", "test", largeSKU)),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// Fan-in rule: a single starved NodePool disables LocalDNS for the NodeClass.
func TestPreferred_AnyStarvedNodePool_Disabled(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(
			newNodePool("large", "test", largeSKU),
			newNodePool("small", "test", smallSKU),
		),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoCompatibleInstanceTypes)
}

// Nothing to evaluate against -- don't commit Enabled, since it would be sticky.
func TestPreferred_NoNodePools_Disabled(t *testing.T) {
	g := NewWithT(t)
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(), defaultInstanceTypeProvider())
	res, err := r.Reconcile(context.Background(), nc)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(res.RequeueAfter).To(Equal(localDNSPreferredRequeueAfter))
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoReferencingNodePools)
}

// A NodePool naming this NodeClass but pointing at a different Group/Kind is
// somebody else's NodeClass and must not be counted.
func TestPreferred_NodePoolWithForeignGroupKind_Ignored(t *testing.T) {
	nc := preferredNC()
	foreign := newNodePool("small", "test", smallSKU)
	foreign.Spec.Template.Spec.NodeClassRef.Group = "karpenter.k8s.aws"
	foreign.Spec.Template.Spec.NodeClassRef.Kind = "EC2NodeClass"
	r := newLocalDNSReconcilerWith(newCRClient(foreign), defaultInstanceTypeProvider())
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	// Ignored entirely rather than counted as starved.
	expectReadyReason(t, nc, reasonNoReferencingNodePools)
}

func TestPreferred_NodePoolForOtherNodeClass_Ignored(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(
			newNodePool("large", "test", largeSKU),
			newNodePool("small", "other-nodeclass", smallSKU),
		),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// An unconstrained NodePool can still reach the large SKU, so the gate passes.
func TestPreferred_UnconstrainedNodePool_Enabled(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("any", "test")),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// Sticky-Enabled still wins: narrowing a NodePool after the fact does not flip
// LocalDNS back off, because that would drift and reimage the pool.
func TestPreferred_StickyEnabled_SurvivesStarvedNodePool(t *testing.T) {
	nc := preferredNC()
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("small", "test", smallSKU)),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_InstanceTypeProviderError_Requeues(t *testing.T) {
	g := NewWithT(t)
	nc := preferredNC()
	itProvider := defaultInstanceTypeProvider()
	itProvider.err = errors.New("boom")
	r := newLocalDNSReconcilerWith(newCRClient(newNodePool("any", "test")), itProvider)

	_, err := r.Reconcile(context.Background(), nc)
	g.Expect(err).To(HaveOccurred())
	g.Expect(nc.StatusConditions().IsTrue(v1beta1.ConditionTypeLocalDNSReady)).To(BeFalse())
}

// The gate must probe the provider with LocalDNS on. If it probed with LocalDNS
// off it would see the unfiltered list, find the small SKU compatible, and
// wrongly enable.
func TestPreferred_GateProbesProviderWithLocalDNSEnabled(t *testing.T) {
	nc := preferredNC()
	small := newInstanceType(smallSKU, 2)
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("small", "test", smallSKU)),
		&stubInstanceTypeProvider{
			all:               []*corecloudprovider.InstanceType{small},
			localDNSSupported: nil,
		},
	)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoCompatibleInstanceTypes)
}
