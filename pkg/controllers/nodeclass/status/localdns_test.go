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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
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

// newNC builds a minimal AKSNodeClass usable directly (avoids dependency on
// the full test helper package which would pull in the envtest suite).
func newNC() *v1beta1.AKSNodeClass {
	nc := &v1beta1.AKSNodeClass{}
	nc.Name = "test"
	nc.Generation = 1
	return nc
}

func setReady(nc *v1beta1.AKSNodeClass, k8sVer string) {
	nc.Status.KubernetesVersion = lo.ToPtr(k8sVer)
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
}

func mustReconcile(t *testing.T, r *LocalDNSReconciler, nc *v1beta1.AKSNodeClass) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), nc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func expectState(t *testing.T, nc *v1beta1.AKSNodeClass, want v1beta1.LocalDNSState) {
	t.Helper()
	if nc.Status.LocalDNSState == nil {
		t.Fatalf("expected LocalDNSState=%q, got nil", want)
	}
	if *nc.Status.LocalDNSState != want {
		t.Fatalf("expected LocalDNSState=%q, got %q", want, *nc.Status.LocalDNSState)
	}
}

func TestModeUnsetClearsState(t *testing.T) {
	nc := newNC()
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	if nc.Status.LocalDNSState != nil {
		t.Fatalf("expected nil state, got %v", *nc.Status.LocalDNSState)
	}
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

func TestPreferred_K8sVersionMissing_WaitsUnknown(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	if nc.Status.LocalDNSState != nil {
		t.Fatalf("expected nil state when KV unresolved")
	}
	if nc.StatusConditions().IsTrue(v1beta1.ConditionTypeLocalDNSReady) {
		t.Fatalf("expected LocalDNSReady not True yet")
	}
}

func TestPreferred_K8sBelowThreshold_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, loK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_BYOCNI_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", consts.NetworkPluginNone)
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_NoConflicts_Enabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_NodeLocalDNSPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
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
	setReady(nc, hiK8s)
	k8sFake := fake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_KonnectivityAgentIgnored(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
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
	setReady(nc, hiK8s)
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	nc.Status.LocalDNSStateObservedGeneration = nc.Generation
	nc.Status.LocalDNSStateObservedKubernetesVersion = hiK8s

	k8sFake := fake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func TestPreferred_K8sVersionBumpReevaluates(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
	nc.Status.LocalDNSStateObservedGeneration = nc.Generation
	nc.Status.LocalDNSStateObservedKubernetesVersion = loK8s
	setReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
	if nc.Status.LocalDNSStateObservedKubernetesVersion != hiK8s {
		t.Fatalf("expected observedKV=%s, got %s", hiK8s, nc.Status.LocalDNSStateObservedKubernetesVersion)
	}
}

func TestPreferred_NoOpOnSameTuple(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
	nc.Status.LocalDNSStateObservedGeneration = nc.Generation
	nc.Status.LocalDNSStateObservedKubernetesVersion = hiK8s
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_TransientErrorRetriesThenFailSafe(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	k8sFake := fake.NewClientset()
	k8sFake.PrependReactor("list", "networkpolicies", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("transient")
	})
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")

	for i := int32(1); i < 3; i++ {
		res, err := r.Reconcile(context.Background(), nc)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.RequeueAfter == 0 {
			t.Fatalf("attempt %d: expected RequeueAfter>0", i)
		}
		if nc.Status.LocalDNSResolveFailures != i {
			t.Fatalf("attempt %d: failures=%d", i, nc.Status.LocalDNSResolveFailures)
		}
		if nc.Status.LocalDNSState != nil {
			t.Fatalf("attempt %d: state should not be committed yet", i)
		}
	}
	// 3rd commits Disabled.
	res, err := r.Reconcile(context.Background(), nc)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue on commit")
	}
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	if nc.Status.LocalDNSResolveFailures != 0 {
		t.Fatalf("expected counter reset, got %d", nc.Status.LocalDNSResolveFailures)
	}
	if !nc.StatusConditions().IsTrue(v1beta1.ConditionTypeLocalDNSReady) {
		t.Fatalf("expected Ready=True after fail-safe commit")
	}
}

func TestPreferred_TransientCounterResetsOnGenerationChange(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	// Pretend we previously committed Disabled at gen=1.
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
	nc.Status.LocalDNSStateObservedGeneration = 1
	nc.Status.LocalDNSStateObservedKubernetesVersion = hiK8s
	nc.Status.LocalDNSResolveFailures = 2 // residual

	// Bump generation, then mark KV-Ready at the new generation.
	nc.Generation = 2
	setReady(nc, hiK8s)
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	if _, err := r.Reconcile(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	if nc.Status.LocalDNSResolveFailures != 0 {
		t.Fatalf("expected counter reset, got %d", nc.Status.LocalDNSResolveFailures)
	}
	if nc.Status.LocalDNSStateObservedGeneration != 2 {
		t.Fatalf("expected observedGen=2, got %d", nc.Status.LocalDNSStateObservedGeneration)
	}
}

func TestPreferred_DaemonSetGetNotFoundIsHandled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	k8sFake := fake.NewClientset()
	k8sFake.PrependReactor("get", "daemonsets", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "node-local-dns")
	})
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateEnabled)
}

func unstructuredPolicy(gvr schema.GroupVersionResource, name, namespace, kind string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: kind})
	u.SetName(name)
	if namespace != "" {
		u.SetNamespace(namespace)
	}
	return u
}

func TestPreferred_CiliumNetworkPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	gvr := schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		gvr: "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}:  "CiliumClusterwideNetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:       "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	}, unstructuredPolicy(gvr, "block-egress", "default", "CiliumNetworkPolicy"))
	r := NewLocalDNSReconciler(fake.NewClientset(), dyn, "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_CiliumClusterwideNetworkPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	gvr := schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:              "CiliumNetworkPolicyList",
		gvr: "CiliumClusterwideNetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:       "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	}, unstructuredPolicy(gvr, "deny-cluster", "", "CiliumClusterwideNetworkPolicy"))
	r := NewLocalDNSReconciler(fake.NewClientset(), dyn, "cilium", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_CalicoNetworkPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	gvr := schema.GroupVersionResource{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:             "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}:  "CiliumClusterwideNetworkPolicyList",
		gvr: "NetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}: "GlobalNetworkPolicyList",
	}, unstructuredPolicy(gvr, "block", "default", "NetworkPolicy"))
	r := NewLocalDNSReconciler(fake.NewClientset(), dyn, "calico", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_CalicoGlobalNetworkPolicyPresent_Disabled(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	gvr := schema.GroupVersionResource{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}:            "CiliumNetworkPolicyList",
		{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}: "CiliumClusterwideNetworkPolicyList",
		{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}:      "NetworkPolicyList",
		gvr: "GlobalNetworkPolicyList",
	}, unstructuredPolicy(gvr, "deny-global", "", "GlobalNetworkPolicy"))
	r := NewLocalDNSReconciler(fake.NewClientset(), dyn, "calico", "azure")
	mustReconcile(t, r, nc)
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
}

func TestPreferred_Forbidden_FailsSafeImmediately(t *testing.T) {
	nc := newNC()
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
	setReady(nc, hiK8s)
	k8sFake := fake.NewClientset()
	k8sFake.PrependReactor("list", "networkpolicies", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Group: "networking.k8s.io", Resource: "networkpolicies"}, "", errors.New("rbac"))
	})
	r := NewLocalDNSReconciler(k8sFake, newDynFake(), "cilium", "azure")
	res, err := r.Reconcile(context.Background(), nc)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue on Forbidden fail-safe")
	}
	expectState(t, nc, v1beta1.LocalDNSStateDisabled)
	if nc.Status.LocalDNSResolveFailures != 0 {
		t.Fatalf("expected no failure counter increment, got %d", nc.Status.LocalDNSResolveFailures)
	}
}

func TestInvalidMode_ClearsState(t *testing.T) {
	nc := newNC()
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	nc.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSMode("Bogus")}
	r := NewLocalDNSReconciler(fake.NewClientset(), newDynFake(), "", "azure")
	mustReconcile(t, r, nc)
	if nc.Status.LocalDNSState != nil {
		t.Fatalf("expected nil state for invalid mode, got %v", *nc.Status.LocalDNSState)
	}
}
