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
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

const (
	// Internal aliases for consts.NetworkPolicyCilium / consts.NetworkPolicyCalico.
	networkPolicyCalico = consts.NetworkPolicyCalico
	networkPolicyCilium = consts.NetworkPolicyCilium
)

// LocalDNSReconciler resolves the effective LocalDNS state on an AKSNodeClass
// and stores it on Status.LocalDNSState.
//
// IMPORTANT: the Preferred-mode gate logic in this file must stay in sync with
// the aks-rp validator at
// resourceprovider/.../validation/localdns/localdnsvalidator.go (source of
// truth). If you add, remove, or reorder a gate here, mirror the change there
// (and update the e2e matrix in the PR description). The wire contract
// guarantees Karpenter resolves Preferred to a terminal value before sending,
// so the nodeprovisioner never re-runs gates -- divergence between this file
// and the RP validator would produce silently inconsistent decisions across
// node-class types.
//
// Behavior:
//   - Mode unset/nil  -> clear Status, LocalDNSReady=True.
//   - Mode=Required   -> Status=Enabled, LocalDNSReady=True.
//   - Mode=Disabled   -> Status=Disabled, LocalDNSReady=True.
//   - Mode=Preferred  -> evaluate five gates (k8s>=1.36, !BYO CNI,
//     !ResolvesToUbuntu2004, no conflicting NetworkPolicies, no upstream
//     node-local-dns DS) and commit Enabled or Disabled. Sticky: once Enabled
//     under Preferred, stays Enabled while Mode=Preferred (read off
//     Status.LocalDNSState directly).
//
// Transient kube-API errors return error so controller-runtime applies
// exponential backoff requeue. No custom retry counter or fail-safe budget --
// the controller keeps retrying until the cluster cooperates.
type LocalDNSReconciler struct {
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	networkPolicy    string
	networkPlugin    string
	versionThreshold semver.Version
}

// NewLocalDNSReconciler constructs a LocalDNSReconciler. dynamicClient may be
// nil in tests that don't exercise the Cilium / Calico CRD path.
func NewLocalDNSReconciler(kubeClient kubernetes.Interface, dynamicClient dynamic.Interface, networkPolicy, networkPlugin string) *LocalDNSReconciler {
	return &LocalDNSReconciler{
		kubeClient:       kubeClient,
		dynamicClient:    dynamicClient,
		networkPolicy:    networkPolicy,
		networkPlugin:    networkPlugin,
		versionThreshold: lo.Must(semver.ParseTolerant(consts.LocalDNSPreferredK8sVersionThreshold)),
	}
}

// Reconcile runs LocalDNS state resolution. It is invoked from the parent
// nodeclass.status Controller, which owns the Status patch.
func (r *LocalDNSReconciler) Reconcile(ctx context.Context, nc *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName("nodeclass.localdns"))

	// Mode unset -> clear Status, mark Ready.
	if nc.Spec.LocalDNS == nil || nc.Spec.LocalDNS.Mode == "" {
		nc.Status.LocalDNSState = nil
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	switch nc.Spec.LocalDNS.Mode {
	case v1beta1.LocalDNSModeRequired:
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil

	case v1beta1.LocalDNSModeDisabled:
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil

	case v1beta1.LocalDNSModePreferred:
		// fall through

	default:
		// Unknown mode: clear Status and mark Ready -- spec validation surfaces
		// the bad value to the user elsewhere.
		nc.Status.LocalDNSState = nil
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	// Sticky-Enabled: if already Enabled under Preferred, keep Enabled.
	if nc.Status.LocalDNSState != nil && *nc.Status.LocalDNSState == v1beta1.LocalDNSStateEnabled {
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	// Static gates first (no kube-API calls).
	if !r.passesStaticGates(nc) {
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	// Cluster gates: any transient error -> return error so controller-runtime
	// requeues with backoff. Don't mark Ready=True.
	ok, err := r.passesClusterGates(ctx)
	if err != nil {
		log.FromContext(ctx).V(1).Info("localdns resolve: transient error, requeuing", "error", err.Error())
		nc.StatusConditions().SetFalse(v1beta1.ConditionTypeLocalDNSReady, "ResolveTransientError", err.Error())
		return reconcile.Result{}, err
	}
	if !ok {
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
	} else {
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	}
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
	return reconcile.Result{}, nil
}

func (r *LocalDNSReconciler) passesStaticGates(nc *v1beta1.AKSNodeClass) bool {
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

// passesClusterGates returns true if cluster-side checks (network policies,
// node-local-dns DS) all pass. Errors are propagated to the caller.
func (r *LocalDNSReconciler) passesClusterGates(ctx context.Context) (bool, error) {
	if strings.EqualFold(r.networkPolicy, networkPolicyCilium) || strings.EqualFold(r.networkPolicy, networkPolicyCalico) {
		conflict, err := r.hasConflictingNetworkPolicies(ctx, r.networkPolicy)
		if err != nil {
			return false, err
		}
		if conflict {
			return false, nil
		}
	}
	has, err := r.hasUpstreamNodeLocalDNS(ctx)
	if err != nil {
		return false, err
	}
	return !has, nil
}

func (r *LocalDNSReconciler) hasUpstreamNodeLocalDNS(ctx context.Context) (bool, error) {
	if r.kubeClient == nil {
		return false, nil
	}
	_, err := r.kubeClient.AppsV1().DaemonSets(consts.NodeLocalDNSDaemonSetNamespace).Get(ctx, consts.NodeLocalDNSDaemonSetName, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("checking node-local-dns daemonset: %w", err)
}

func (r *LocalDNSReconciler) hasConflictingNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
	if r.kubeClient == nil {
		return false, nil
	}
	if conflict, err := r.hasConflictingK8sNetworkPolicies(ctx); err != nil || conflict {
		return conflict, err
	}
	return r.hasConflictingCRDNetworkPolicies(ctx, networkPolicyType)
}

func (r *LocalDNSReconciler) hasConflictingK8sNetworkPolicies(ctx context.Context) (bool, error) {
	// Limit:2 is sufficient: konnectivity-agent is uniquely named, so any
	// response with 2 items guarantees at least one non-allow-listed policy
	// (i.e. a real conflict). A response with 1 item that is konnectivity is
	// proof there are no conflicting policies; 0 items is obviously clean.
	// No pagination needed.
	netPolList, err := r.kubeClient.NetworkingV1().NetworkPolicies("").List(ctx, metav1.ListOptions{Limit: 2})
	if err != nil {
		return false, fmt.Errorf("listing K8s NetworkPolicies: %w", err)
	}
	for _, np := range netPolList.Items {
		if np.Name == consts.KonnectivityAgentPolicyName && np.Namespace == consts.KonnectivityAgentPolicyNamespace {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *LocalDNSReconciler) hasConflictingCRDNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
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
			// CRD not installed on the cluster -- treat as no conflicting
			// policies of this type rather than surfacing as a transient error.
			if k8serrors.IsNotFound(err) || meta.IsNoMatchError(err) {
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
