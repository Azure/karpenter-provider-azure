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

package localdns_test

import (
	"context"
	"errors"
	"testing"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/localdns"
)

// newNodeClass returns a Preferred-mode AKSNodeClass with the given K8s version
// stamped on Status, and the KubernetesVersionReady condition set true with
// ObservedGeneration matching the NodeClass Generation — required for
// GetKubernetesVersion() to return the value.
func newNodeClass(k8sVersion string) *v1beta1.AKSNodeClass {
	nc := &v1beta1.AKSNodeClass{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Generation: 1},
		Spec: v1beta1.AKSNodeClassSpec{
			LocalDNS:    &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred},
			ImageFamily: lo.ToPtr("Ubuntu2204"),
		},
	}
	nc.Status.KubernetesVersion = lo.ToPtr(k8sVersion)
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
	return nc
}

func newCRClient(nc *v1beta1.AKSNodeClass) client.Client {
	scheme := runtime.NewScheme()
	_ = v1beta1.SchemeBuilder.AddToScheme(scheme)
	return crfake.NewClientBuilder().WithScheme(scheme).WithObjects(nc).Build()
}

// dynamicSchemeWithCRDs registers list kinds for the CRD-network-policy GVRs
// so the dynamic fake client can serve List() calls.
func dynamicSchemeWithCRDs() *runtime.Scheme {
	s := runtime.NewScheme()
	registerListKind := func(gvr schema.GroupVersionResource, listKind string) {
		s.AddKnownTypeWithName(
			gvr.GroupVersion().WithKind(listKind),
			&unstructured.UnstructuredList{},
		)
	}
	registerListKind(schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"}, "CiliumNetworkPolicyList")
	registerListKind(schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"}, "CiliumClusterwideNetworkPolicyList")
	registerListKind(schema.GroupVersionResource{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"}, "NetworkPolicyList")
	registerListKind(schema.GroupVersionResource{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"}, "GlobalNetworkPolicyList")
	return s
}

func unstructuredItem(apiVersion, kind, ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

// ---- Static gates ---------------------------------------------------------

func TestResolvePreferred_StaticGate_OldK8s_Disabled(t *testing.T) {
	nc := newNodeClass("1.35.0")
	r := localdns.NewResolver(kubefake.NewClientset(), dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled for K8s 1.35, got %s", got)
	}
}

func TestResolvePreferred_StaticGate_BYOCNI_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	r := localdns.NewResolver(kubefake.NewClientset(), dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "none")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled for BYO CNI, got %s", got)
	}
}

func TestResolvePreferred_AllGatesPass_Enabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	r := localdns.NewResolver(kubefake.NewClientset(), dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateEnabled {
		t.Fatalf("expected Enabled, got %s", got)
	}
	if nc.Annotations[v1beta1.AnnotationLocalDNSState] != string(v1beta1.LocalDNSStateEnabled) {
		t.Fatalf("expected Enabled annotation written, got %q", nc.Annotations[v1beta1.AnnotationLocalDNSState])
	}
}

// ---- Cluster gates: K8s typed NetworkPolicy --------------------------------

func TestResolvePreferred_K8sNetworkPolicy_OnlyKonnectivity_Enabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "konnectivity-agent", Namespace: "kube-system"},
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateEnabled {
		t.Fatalf("expected Enabled (konnectivity excluded), got %s", got)
	}
}

func TestResolvePreferred_K8sNetworkPolicy_Conflict_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled (user NP conflict), got %s", got)
	}
}

func TestResolvePreferred_K8sNetworkPolicy_NPM_Bypassed_Enabled(t *testing.T) {
	// On NPM clusters the K8s typed NP gate is intentionally skipped.
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "azure", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateEnabled {
		t.Fatalf("expected Enabled (NPM bypass), got %s", got)
	}
}

// ---- Cluster gates: CRD NetworkPolicies -----------------------------------

func TestResolvePreferred_CiliumNetworkPolicy_Conflict_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	dc := dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs(),
		unstructuredItem("cilium.io/v2", "CiliumNetworkPolicy", "default", "my-cnp"))
	r := localdns.NewResolver(kubefake.NewClientset(), dc, newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled (cilium CNP), got %s", got)
	}
}

func TestResolvePreferred_CiliumClusterwideNetworkPolicy_Conflict_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	dc := dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs(),
		unstructuredItem("cilium.io/v2", "CiliumClusterwideNetworkPolicy", "", "my-ccnp"))
	r := localdns.NewResolver(kubefake.NewClientset(), dc, newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled (cilium CCNP), got %s", got)
	}
}

func TestResolvePreferred_CalicoNetworkPolicy_Conflict_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	dc := dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs(),
		unstructuredItem("crd.projectcalico.org/v1", "NetworkPolicy", "default", "my-calico-np"))
	r := localdns.NewResolver(kubefake.NewClientset(), dc, newCRClient(nc), "calico", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled (calico NP), got %s", got)
	}
}

func TestResolvePreferred_CalicoGlobalNetworkPolicy_Conflict_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	dc := dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs(),
		unstructuredItem("crd.projectcalico.org/v1", "GlobalNetworkPolicy", "", "my-calico-gnp"))
	r := localdns.NewResolver(kubefake.NewClientset(), dc, newCRClient(nc), "calico", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled (calico GNP), got %s", got)
	}
}

// ---- Cluster gates: node-local-dns DaemonSet ------------------------------

func TestResolvePreferred_NodeLocalDNSDaemonSetPresent_Disabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset(&appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-local-dns", Namespace: "kube-system"},
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled (node-local-dns DS), got %s", got)
	}
}

// ---- Fail-safe on transient kube-API errors -------------------------------

func TestResolvePreferred_NetworkPolicyListError_FailSafeDisabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset()
	kc.PrependReactor("list", "networkpolicies", func(_ clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("kube API blip")
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled on NetworkPolicy list error, got %s", got)
	}
}

func TestResolvePreferred_DaemonSetGetError_FailSafeDisabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset()
	kc.PrependReactor("get", "daemonsets", func(_ clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("kube API blip")
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled on DaemonSet get error, got %s", got)
	}
}

func TestResolvePreferred_DaemonSetForbidden_FailSafeDisabled(t *testing.T) {
	nc := newNodeClass("1.36.0")
	kc := kubefake.NewClientset()
	kc.PrependReactor("get", "daemonsets", func(_ clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "node-local-dns", errors.New("no perm"))
	})
	r := localdns.NewResolver(kc, dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateDisabled {
		t.Fatalf("expected Disabled on DaemonSet Forbidden, got %s", got)
	}
}

// ---- Persistence: Enabled and Disabled both written -----------------------

func TestResolvePreferred_PersistsDisabledAnnotation(t *testing.T) {
	nc := newNodeClass("1.35.0") // static gate fails
	r := localdns.NewResolver(kubefake.NewClientset(), dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), newCRClient(nc), "cilium", "azure")
	r.ResolvePreferred(context.Background(), nc)
	if nc.Annotations[v1beta1.AnnotationLocalDNSState] != string(v1beta1.LocalDNSStateDisabled) {
		t.Fatalf("expected Disabled annotation written, got %q", nc.Annotations[v1beta1.AnnotationLocalDNSState])
	}
}

func TestResolvePreferred_NilCRClient_NoPanic(t *testing.T) {
	nc := newNodeClass("1.36.0")
	r := localdns.NewResolver(kubefake.NewClientset(), dynamicfake.NewSimpleDynamicClient(dynamicSchemeWithCRDs()), nil, "cilium", "azure")
	if got := r.ResolvePreferred(context.Background(), nc); got != v1beta1.LocalDNSStateEnabled {
		t.Fatalf("expected Enabled, got %s", got)
	}
	// No annotation persistence attempted; map should be nil/empty.
	if v, ok := nc.Annotations[v1beta1.AnnotationLocalDNSState]; ok {
		t.Fatalf("expected no annotation written when crClient is nil, got %q", v)
	}
}
