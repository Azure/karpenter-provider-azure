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
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

const (
	hiK8s = "1.36.0"
	loK8s = "1.35.0"
)

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
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	if !nc.StatusConditions().IsTrue(v1beta1.ConditionTypeLocalDNSReady) {
		t.Fatalf("expected LocalDNSReady=True")
	}
}

func TestModeRequired(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestModeDisabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_K8sBelowThreshold_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, loK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_BYOCNI_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", consts.NetworkPluginNone)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_Ubuntu2004_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	nc.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
	nc.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
	setKVReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_NoConflicts_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setKVReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "cilium", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
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
	r := NewLocalDNSReconciler(fake.NewClientset(), dc, "cilium", "azure")
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
	r := NewLocalDNSReconciler(fake.NewClientset(), dc, "calico", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "", "azure")
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
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "", "azure")
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
	r := NewLocalDNSReconciler(fake.NewClientset(), dc, "cilium", "azure")
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
	r := NewLocalDNSReconciler(fake.NewClientset(), dc, "calico", "azure")
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
	r := NewLocalDNSReconciler(fake.NewClientset(), dc, "cilium", "azure")
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
	r := NewLocalDNSReconciler(fake.NewClientset(), dc, "calico", "azure")
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
