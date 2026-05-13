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

package localdns

import (
	"context"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

const (
	// PreferredK8sVersionThreshold is the minimum Kubernetes version required to
	// auto-enable LocalDNS when Mode=Preferred.
	PreferredK8sVersionThreshold = "1.36.0"

	networkPolicyCalico = "calico"
	networkPolicyCilium = "cilium"

	konnectivityAgentPolicyName      = "konnectivity-agent"
	konnectivityAgentPolicyNamespace = "kube-system"

	nodeLocalDNSDaemonSetName      = "node-local-dns"
	nodeLocalDNSDaemonSetNamespace = "kube-system"
)

// Resolver computes the resolved LocalDNS state for a NodeClass in Preferred mode.
//
// SYNC: LocalDNS Preferred-mode resolution lives in three places that must stay
// aligned. If any gate changes, update the others in lockstep:
//  1. RP validator (source of truth):
//     resourceprovider/.../validation/localdns/localdnsvalidator.go
//     resolvePreferredState — full gate set: toggle, K8s ver, SKU CPU/mem,
//     IsLocalDNSSupported (Windows / Ubuntu2004 / AvailabilitySets / CustomImage),
//     BYO CNI, NetworkPolicy, node-local-dns DaemonSet.
//  2. Nodeprovisioner: nodeprovisioner/server/models/convertto.go
//     resolvePreferredState — mirrors per-AP gates for the getNodeBootstrapping
//     path. No kube client; cluster-wide checks deferred to the RP validator.
//  3. This resolver — drives Karpenter's instance-type filtering, cache key,
//     node label, and the AnnotationLocalDNSState annotation on the NodeClass.
type Resolver struct {
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	crClient         client.Client
	networkPolicy    string
	networkPlugin    string
	versionThreshold semver.Version
}

// NewResolver constructs a Resolver. dynamicClient may be nil in tests that don't
// exercise the Cilium / Calico CRD path. crClient is used to patch the
// AnnotationLocalDNSState annotation when the resolver lands on Enabled.
func NewResolver(kubeClient kubernetes.Interface, dynamicClient dynamic.Interface, crClient client.Client, networkPolicy, networkPlugin string) *Resolver {
	return &Resolver{
		kubeClient:       kubeClient,
		dynamicClient:    dynamicClient,
		crClient:         crClient,
		networkPolicy:    networkPolicy,
		networkPlugin:    networkPlugin,
		versionThreshold: lo.Must(semver.ParseTolerant(PreferredK8sVersionThreshold)),
	}
}

// ResolvePreferred returns the resolved LocalDNS state for a NodeClass whose
// Spec.LocalDNS.Mode is Preferred. It evaluates the same five gates the RP
// validator runs:
//  1. k8s version >= PreferredK8sVersionThreshold
//  2. BYO CNI excluded
//  3. ResolvesToUbuntu2004 excluded
//  4. No conflicting NetworkPolicies (typed + Cilium/Calico CRDs)
//  5. No upstream kube-system/node-local-dns DaemonSet
//
// Transient kube-API errors (including RBAC Forbidden) fail-safe to Disabled.
// The resolved state is always persisted to AnnotationLocalDNSState — Enabled
// for sticky-Enabled semantics, Disabled so the wire-payload rewrite
// (AKSNodeClass.ResolvedLocalDNSForWire) sees a concrete decision and doesn't
// let nodeprovisioner / the RP-side validator re-resolve to a different
// answer per machine. Disabled is NOT sticky at the read side: IsLocalDNSEnabled
// only short-circuits on the Enabled annotation, so a Disabled outcome is
// re-evaluated on the next call and the annotation gets overwritten if gates
// pass.
func (r *Resolver) ResolvePreferred(ctx context.Context, nc *v1beta1.AKSNodeClass) v1beta1.LocalDNSState {
	state := v1beta1.LocalDNSStateEnabled
	if !r.passesStaticGates(nc) || !r.passesClusterGates(ctx) {
		state = v1beta1.LocalDNSStateDisabled
	}
	r.persistState(ctx, nc, state)
	return state
}

// persistState patches the NodeClass to set AnnotationLocalDNSState to the
// resolved state. Best-effort: failure is logged and ignored — the next
// resolution will retry.
func (r *Resolver) persistState(ctx context.Context, nc *v1beta1.AKSNodeClass, state v1beta1.LocalDNSState) {
	if r.crClient == nil {
		return
	}
	if nc.Annotations[v1beta1.AnnotationLocalDNSState] == string(state) {
		return
	}
	stored := nc.DeepCopy()
	if nc.Annotations == nil {
		nc.Annotations = map[string]string{}
	}
	nc.Annotations[v1beta1.AnnotationLocalDNSState] = string(state)
	if err := r.crClient.Patch(ctx, nc, client.MergeFrom(stored)); err != nil {
		log.FromContext(ctx).V(1).Info("localdns resolve: failed to persist state annotation (will retry on next provisioning)", "state", string(state), "error", err.Error())
	}
}

// passesStaticGates runs the gates that need no kube-API access: K8s version,
// BYO CNI, image family.
func (r *Resolver) passesStaticGates(nc *v1beta1.AKSNodeClass) bool {
	k8sVersion, err := nc.GetKubernetesVersion()
	if err != nil || k8sVersion == "" {
		return false
	}
	parsed, err := semver.ParseTolerant(strings.TrimPrefix(k8sVersion, "v"))
	if err != nil || parsed.LT(r.versionThreshold) {
		return false
	}
	if strings.EqualFold(r.networkPlugin, consts.NetworkPluginNone) {
		return false
	}
	if imagefamily.ResolvesToUbuntu2004(nc.Spec.ImageFamily, nc.Spec.FIPSMode) {
		return false
	}
	return true
}

// passesClusterGates runs the kube-API-dependent gates: conflicting
// NetworkPolicies and upstream node-local-dns DaemonSet. Transient errors
// fail-safe to Disabled (returns false).
func (r *Resolver) passesClusterGates(ctx context.Context) bool {
	if strings.EqualFold(r.networkPolicy, networkPolicyCilium) || strings.EqualFold(r.networkPolicy, networkPolicyCalico) {
		conflict, err := r.hasConflictingNetworkPolicies(ctx, r.networkPolicy)
		if err != nil {
			log.FromContext(ctx).V(1).Info("localdns resolve: network policy check failed, defaulting to Disabled", "error", err.Error())
			return false
		}
		if conflict {
			return false
		}
	}
	has, err := r.hasUpstreamNodeLocalDNS(ctx)
	if err != nil {
		log.FromContext(ctx).V(1).Info("localdns resolve: node-local-dns DS check failed, defaulting to Disabled", "error", err.Error())
		return false
	}
	return !has
}

func (r *Resolver) hasUpstreamNodeLocalDNS(ctx context.Context) (bool, error) {
	if r.kubeClient == nil {
		return false, nil
	}
	_, err := r.kubeClient.AppsV1().DaemonSets(nodeLocalDNSDaemonSetNamespace).Get(ctx, nodeLocalDNSDaemonSetName, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("checking node-local-dns daemonset: %w", err)
}

func (r *Resolver) hasConflictingNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
	if r.kubeClient == nil {
		return false, nil
	}
	if conflict, err := r.hasConflictingK8sNetworkPolicies(ctx); err != nil || conflict {
		return conflict, err
	}
	return r.hasConflictingCRDNetworkPolicies(ctx, networkPolicyType)
}

func (r *Resolver) hasConflictingK8sNetworkPolicies(ctx context.Context) (bool, error) {
	netPolList, err := r.kubeClient.NetworkingV1().NetworkPolicies("").List(ctx, metav1.ListOptions{Limit: 2})
	if err != nil {
		return false, fmt.Errorf("listing K8s NetworkPolicies: %w", err)
	}
	for _, np := range netPolList.Items {
		if np.Name == konnectivityAgentPolicyName && np.Namespace == konnectivityAgentPolicyNamespace {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *Resolver) hasConflictingCRDNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
	if r.dynamicClient == nil {
		return false, nil
	}
	var crdResources []schema.GroupVersionResource
	switch {
	case strings.EqualFold(networkPolicyType, networkPolicyCilium):
		crdResources = []schema.GroupVersionResource{
			{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"},
			{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"},
		}
	case strings.EqualFold(networkPolicyType, networkPolicyCalico):
		crdResources = []schema.GroupVersionResource{
			{Group: "crd.projectcalico.org", Version: "v1", Resource: "networkpolicies"},
			{Group: "crd.projectcalico.org", Version: "v1", Resource: "globalnetworkpolicies"},
		}
	}
	for _, gvr := range crdResources {
		list, err := r.dynamicClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("listing %s: %w", gvr.Resource, err)
		}
		if len(list.Items) > 0 {
			return true, nil
		}
	}
	return false, nil
}
