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
	"k8s.io/apimachinery/pkg/api/resource"
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
	"sigs.k8s.io/karpenter/pkg/controllers/nodeoverlay"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

const (
	hiK8s = "1.99.0"
	loK8s = "1.98.0"

	smallSKU = "Standard_B2s"    // 2 vCPU -- below the LocalDNS floor
	largeSKU = "Standard_D4s_v3" // 4 vCPU -- above the LocalDNS floor
)

// stubInstanceTypeProvider stands in for the real instance type provider. It
// mirrors the one behavior this reconciler depends on: List returns the
// LocalDNS-filtered set only when the NodeClass it is handed reports LocalDNS
// as enabled. That makes the probe in checkInstanceTypeGate
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
		// Capacity is what the NodePool limits check reads.
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(int64(cpu), resource.DecimalSI),
		},
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
	r := NewLocalDNSReconciler(
		kubeClient,
		dynamicClient,
		newCRClient(newNodePool("default", "test")),
		defaultInstanceTypeProvider(),
		nil, // NodeOverlays are exercised separately, below.
		networkPolicy,
		networkPlugin,
	)
	return r
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
	_, err := r.Reconcile(testCtx(), nc)
	g.Expect(err).ToNot(HaveOccurred())
}

// mustResolve drives Preferred resolution to its settled outcome.
func mustResolve(t *testing.T, r *LocalDNSReconciler, nc *v1beta1.AKSNodeClass) {
	t.Helper()
	mustReconcile(t, r, nc)
}

// testCtx carries core's options, which checkInstanceTypeGate reads for the
// minValues policy. coreoptions.FromContext panics when they are absent, and
// production always has them injected by the operator.
func testCtx() context.Context {
	return coreoptions.ToContext(context.Background(), coretest.Options())
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
	mustResolve(t, r, nc)
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
	mustResolve(t, r, nc)
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
	mustResolve(t, r, nc)
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
	mustResolve(t, r, nc)
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
	mustResolve(t, r, nc)
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
	mustResolve(t, r, nc)
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
	return newLocalDNSReconcilerWithStore(crClient, itProvider, nil)
}

func newLocalDNSReconcilerWithStore(crClient client.Client, itProvider instancetype.Provider, store *nodeoverlay.InstanceTypeStore) *LocalDNSReconciler {
	return NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), crClient, itProvider, store, "", "azure")
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
	mustResolve(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// Core folds spec.template.metadata.labels into each NodeClaim's scheduling
// requirements (scheduling.NewNodeClaimTemplate), so a pool that pins its VM
// size through labels is as constrained as one that pins it through
// requirements. Reading only spec.requirements would see an unconstrained pool
// and wrongly enable LocalDNS -- stickily -- leaving the pool with no
// provisionable size.
func TestPreferred_NodePoolPinnedToSmallSKUViaTemplateLabels_Disabled(t *testing.T) {
	np := newNodePool("small-by-label", "test")
	np.Spec.Template.Labels = map[string]string{corev1.LabelInstanceTypeStable: smallSKU}

	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(np), defaultInstanceTypeProvider())
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoCompatibleInstanceTypes)
}

// The mirror case: label-derived requirements must not over-restrict either. A
// pool labeled to a size above the floor still has candidates, and unrelated
// labels the instance types say nothing about must not narrow the match.
func TestPreferred_NodePoolPinnedToLargeSKUViaTemplateLabels_Enabled(t *testing.T) {
	np := newNodePool("large-by-label", "test")
	np.Spec.Template.Labels = map[string]string{
		corev1.LabelInstanceTypeStable: largeSKU,
		"team":                         "infra",
	}

	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(np), defaultInstanceTypeProvider())
	mustResolve(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// A non-empty candidate set does not prove the pool can provision. Core
// applies minValues to the surviving set, so a pool listing one sub-floor and
// one above-floor SKU with minValues 2 is compatible pairwise yet unprovisionable
// once the LocalDNS filter removes the small one.
func TestPreferred_NodePoolMinValuesUnsatisfiable_Disabled(t *testing.T) {
	np := newNodePool("min-values", "test")
	np.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{{
		Key:       corev1.LabelInstanceTypeStable,
		Operator:  corev1.NodeSelectorOpIn,
		Values:    []string{smallSKU, largeSKU},
		MinValues: lo.ToPtr(2),
	}}

	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(np), defaultInstanceTypeProvider())
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoCompatibleInstanceTypes)
}

// Under BestEffort core relaxes minValues rather than failing, so the same pool
// can still provision and must not be called starved.
func TestPreferred_NodePoolMinValuesUnsatisfiableBestEffort_Enabled(t *testing.T) {
	np := newNodePool("min-values-best-effort", "test")
	np.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{{
		Key:       corev1.LabelInstanceTypeStable,
		Operator:  corev1.NodeSelectorOpIn,
		Values:    []string{smallSKU, largeSKU},
		MinValues: lo.ToPtr(2),
	}}

	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(np), defaultInstanceTypeProvider())
	opts := coretest.Options()
	opts.MinValuesPolicy = coreoptions.MinValuesPolicyBestEffort
	ctx := coreoptions.ToContext(context.Background(), opts)

	g := NewWithT(t)
	_, err := r.Reconcile(ctx, nc)
	g.Expect(err).ToNot(HaveOccurred())
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// Limits are a static ceiling on a single node: if every SKU meeting the
// LocalDNS floor alone exceeds the pool's whole cpu limit, core's
// filterByRemainingResources leaves nothing to provision.
func TestPreferred_NodePoolLimitsExcludeAllSupported_Disabled(t *testing.T) {
	np := newNodePool("tight-limits", "test")
	np.Spec.Limits = karpv1.Limits{corev1.ResourceCPU: resource.MustParse("2")}

	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(np), defaultInstanceTypeProvider())
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	expectReadyReason(t, nc, reasonNoCompatibleInstanceTypes)
}

// The mirror case: a limit high enough for a single node is a cumulative cap,
// not a per-node one, and must not be read as starvation.
func TestPreferred_NodePoolRoomyLimits_Enabled(t *testing.T) {
	np := newNodePool("roomy-limits", "test")
	np.Spec.Limits = karpv1.Limits{corev1.ResourceCPU: resource.MustParse("64")}

	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(np), defaultInstanceTypeProvider())
	mustResolve(t, r, nc)
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
	mustResolve(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

// An unconstrained NodePool can still reach the large SKU, so the gate passes.
func TestPreferred_UnconstrainedNodePool_Enabled(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("any", "test")),
		defaultInstanceTypeProvider(),
	)
	mustResolve(t, r, nc)
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

// --- NodePool watch mapping ---
//
// The instance type gate reads the set of NodePools referencing the NodeClass,
// so NodePool churn has to re-enqueue that NodeClass. Without the mapping the
// gate stays latched on whatever the first reconcile saw until the next
// periodic requeue, and nodes provision with LocalDNS off in the meantime.

func TestNodePoolToAKSNodeClass_MapsToReferencedNodeClass(t *testing.T) {
	g := NewWithT(t)
	reqs := nodePoolToAKSNodeClass(context.Background(), newNodePool("np", "test", largeSKU))
	g.Expect(reqs).To(HaveLen(1))
	g.Expect(reqs[0].Name).To(Equal("test"))
	// AKSNodeClass is cluster-scoped; a namespace here would never match.
	g.Expect(reqs[0].Namespace).To(BeEmpty())
}

// Same Name, different provider: mapping it would make foreign NodePool churn
// spin this controller, and pairs with the gate ignoring it entirely.
func TestNodePoolToAKSNodeClass_IgnoresForeignGroupKind(t *testing.T) {
	g := NewWithT(t)
	foreign := newNodePool("np", "test", largeSKU)
	foreign.Spec.Template.Spec.NodeClassRef.Group = "karpenter.k8s.aws"
	foreign.Spec.Template.Spec.NodeClassRef.Kind = "EC2NodeClass"
	g.Expect(nodePoolToAKSNodeClass(context.Background(), foreign)).To(BeEmpty())
}

func TestNodePoolToAKSNodeClass_IgnoresNilRefAndWrongType(t *testing.T) {
	g := NewWithT(t)
	noRef := newNodePool("np", "test", largeSKU)
	noRef.Spec.Template.Spec.NodeClassRef = nil
	g.Expect(nodePoolToAKSNodeClass(context.Background(), noRef)).To(BeEmpty())
	g.Expect(nodePoolToAKSNodeClass(context.Background(), newNC())).To(BeEmpty())
}

// --- no LocalDNS verdict may cost the cluster its readiness ---

// LocalDNSReady is one of the AKSNodeClass readiness conditions, so reporting
// False stops the NodeClass provisioning nodes at all. No LocalDNS verdict is
// worth that: the user never asked for LocalDNS, and leaving it off is exactly
// how the cluster behaved before the version upgrade made it eligible. Breaking
// provisioning to avoid a node launching without LocalDNS would be AKS causing
// the outage these gates exist to prevent.
func TestPreferred_NoReferencingNodePools_StaysReady(t *testing.T) {
	g := NewWithT(t)
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(newCRClient(), defaultInstanceTypeProvider())
	mustReconcile(t, r, nc)
	cond := nc.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
	g.Expect(cond).ToNot(BeNil())
	g.Expect(cond.IsTrue()).To(BeTrue(), "an undecided LocalDNS verdict must not block provisioning")
	g.Expect(cond.Reason).To(Equal(reasonNoReferencingNodePools))
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

// A terminal decision is ready for the same reason: the gate ran and answered.
func TestPreferred_StarvedNodePool_IsReady(t *testing.T) {
	g := NewWithT(t)
	nc := preferredNC()
	r := newLocalDNSReconcilerWith(
		newCRClient(newNodePool("small", "test", smallSKU)),
		defaultInstanceTypeProvider(),
	)
	mustReconcile(t, r, nc)
	cond := nc.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
	g.Expect(cond).ToNot(BeNil())
	g.Expect(cond.IsTrue()).To(BeTrue())
	g.Expect(cond.Reason).To(Equal(reasonNoCompatibleInstanceTypes))
}

// --- condition message bounds ---

// Condition messages are capped at 32768 characters by the CRD. Listing every
// starved NodePool by name overruns that in a large cluster, the status patch is
// rejected, and the decision is never persisted.
func TestPreferred_ManyStarvedNodePools_MessageBounded(t *testing.T) {
	g := NewWithT(t)
	nc := preferredNC()
	// Max-length (253-char) names, so the unbounded message would be ~64KB.
	longName := func(i int) string { return fmt.Sprintf("%0*d", 253, i) }
	pools := make([]*karpv1.NodePool, 0, 250)
	for i := range 250 {
		pools = append(pools, newNodePool(longName(i), "test", smallSKU))
	}
	r := newLocalDNSReconcilerWith(newCRClient(pools...), defaultInstanceTypeProvider())
	mustReconcile(t, r, nc)
	cond := nc.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
	g.Expect(cond).ToNot(BeNil())
	g.Expect(len(cond.Message)).To(BeNumerically("<", 32768))
	g.Expect(cond.Message).To(ContainSubstring(fmt.Sprintf("(and %d more)", 250-maxNamedStarvedNodePools)))
	// Sorted, so the message is stable across reconciles instead of rewriting
	// the condition every time List returns a different order.
	g.Expect(cond.Message).To(ContainSubstring(longName(0)))
	g.Expect(cond.Message).ToNot(ContainSubstring(longName(249)))
}

func TestSummarizeNodePoolNames_SortsAndKeepsShortListsWhole(t *testing.T) {
	g := NewWithT(t)
	g.Expect(summarizeNodePoolNames([]string{"c", "a", "b"})).To(Equal("a, b, c"))
	g.Expect(summarizeNodePoolNames(nil)).To(BeEmpty())
}

// --- NodeOverlays ---

// The gate has to judge NodePools against the list the scheduler will see.
// NodeOverlays rewrite that list per NodePool -- adding extended resources and
// dropping types outright -- so reading the raw provider list would score a pool
// against instance types it will never be offered.
func TestPreferred_NodeOverlayEnabled_UnevaluatedNodePool_Requeues(t *testing.T) {
	g := NewWithT(t)
	nc := preferredNC()
	// A fresh store has evaluated no NodePools, which is exactly the startup
	// state: ApplyAll reports UnevaluatedNodePoolError and the pool cannot
	// provision from any list yet, so the gate must defer rather than decide.
	r := newLocalDNSReconcilerWithStore(
		newCRClient(newNodePool("large", "test", largeSKU)),
		defaultInstanceTypeProvider(),
		nodeoverlay.NewInstanceTypeStore(),
	)
	opts := coretest.Options()
	opts.FeatureGates.NodeOverlay = true
	_, err := r.Reconcile(coreoptions.ToContext(context.Background(), opts), nc)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("large"))
	// Nothing latched: the next reconcile re-decides from scratch.
	g.Expect(nc.Status.LocalDNSState).To(BeNil())
}

// With the feature gate off the store is not consulted at all, matching
// cloudprovider.resolveInstanceTypes.
func TestPreferred_NodeOverlayDisabled_StoreIgnored(t *testing.T) {
	nc := preferredNC()
	r := newLocalDNSReconcilerWithStore(
		newCRClient(newNodePool("large", "test", largeSKU)),
		defaultInstanceTypeProvider(),
		nodeoverlay.NewInstanceTypeStore(),
	)
	mustResolve(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}
