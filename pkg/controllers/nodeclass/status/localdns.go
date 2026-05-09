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
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

const (
	localDNSReconcilerName = "nodeclass.localdns"

	// localDNSPreferredK8sVersionThreshold is the minimum Kubernetes version required
	// to auto-enable LocalDNS when Mode=Preferred.
	localDNSPreferredK8sVersionThreshold = "1.36.0"

	// localDNSResolveTimeout bounds the total time spent on kube API calls
	// (network policy check + node-local-dns check) during a single resolve attempt.
	localDNSResolveTimeout = 15 * time.Second

	// localDNSMaxResolveFailures is the number of consecutive transient failures
	// the reconciler will tolerate before fail-safing to Disabled and unblocking
	// provisioning. With localDNSResolveBackoff this caps the worst-case delay
	// at localDNSMaxResolveFailures * localDNSResolveBackoff.
	localDNSMaxResolveFailures = 3

	// localDNSResolveBackoff is the requeue interval between transient failures.
	localDNSResolveBackoff = 10 * time.Second

	// Network policy values used by AKS clusters; matched case-insensitively against
	// options.NetworkPolicy.
	networkPolicyCalico = "calico"
	networkPolicyCilium = "cilium"

	// konnectivityAgentPolicyName is the AKS-managed NetworkPolicy in kube-system
	// that exists by default on some clusters; it should not block LocalDNS.
	konnectivityAgentPolicyName      = "konnectivity-agent"
	konnectivityAgentPolicyNamespace = "kube-system"

	nodeLocalDNSDaemonSetName      = "node-local-dns"
	nodeLocalDNSDaemonSetNamespace = "kube-system"
)

// LocalDNSReconciler resolves the effective LocalDNS state on an AKSNodeClass.
//
// Behavior summary (see PR description / design doc for full matrix):
//   - Mode=Required → state=Enabled (mirrored from spec).
//   - Mode=Disabled → state=Disabled (mirrored from spec).
//   - Mode=Preferred → state resolved by safety checks (k8s version, BYO CNI,
//     network policy presence, upstream node-local-dns presence). Resolution
//     runs at NodeClass create/update and on observed Kubernetes version
//     changes. Once Enabled under Preferred, the state is sticky and never
//     auto-flips back to Disabled — users must opt out via Spec.LocalDNS.Mode.
//   - Transient kube-API errors are retried up to localDNSMaxResolveFailures
//     times with localDNSResolveBackoff between attempts; on exhaustion the
//     reconciler commits state=Disabled (fail-safe) so provisioning is not
//     blocked indefinitely.
//
// LocalDNSReady condition is rolled into the AKSNodeClass aggregate Ready
// condition; the Karpenter core provisioner defers scheduling NodeClaims
// against NodePools whose NodeClass is not Ready.
type LocalDNSReconciler struct {
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	networkPolicy    string
	networkPlugin    string
	versionThreshold semver.Version
	maxFailures      int32
	failureBackoff   time.Duration
	resolveTimeout   time.Duration
}

// NewLocalDNSReconciler constructs a LocalDNSReconciler.
//
// dynamicClient is used to list cilium/calico CRD-based network policies; it
// may be nil only in tests where the Preferred-mode CRD path is not exercised.
func NewLocalDNSReconciler(kubeClient kubernetes.Interface, dynamicClient dynamic.Interface, networkPolicy, networkPlugin string) *LocalDNSReconciler {
	return &LocalDNSReconciler{
		kubeClient:       kubeClient,
		dynamicClient:    dynamicClient,
		networkPolicy:    networkPolicy,
		networkPlugin:    networkPlugin,
		versionThreshold: lo.Must(semver.ParseTolerant(localDNSPreferredK8sVersionThreshold)),
		maxFailures:      localDNSMaxResolveFailures,
		failureBackoff:   localDNSResolveBackoff,
		resolveTimeout:   localDNSResolveTimeout,
	}
}

func (r *LocalDNSReconciler) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(localDNSReconcilerName).
		For(&v1beta1.AKSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), r))
}

// Reconcile implements the LocalDNS state resolution and stickiness rules.
func (r *LocalDNSReconciler) Reconcile(ctx context.Context, nc *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName(localDNSReconcilerName))

	if r.handleSimpleModes(nc) {
		return reconcile.Result{}, nil
	}

	// Mode=Preferred from here. Need Status.KubernetesVersion to make a decision.
	kv, err := nc.GetKubernetesVersion()
	if err != nil {
		// KubernetesVersion not yet resolved by its own sub-reconciler.
		// Keep the condition Unknown (default initial state) and wait for the
		// kubernetesversion reconciler to write status — which will trigger
		// another reconcile since the controller watches the AKSNodeClass.
		nc.StatusConditions().SetUnknown(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	// Reset transient failure counter when (gen, k8s) tuple changes after a
	// prior commit; this also implicitly resets it on every successful commit
	// below. We require LocalDNSState != nil so we don't keep zeroing the
	// counter between transient retries on a brand-new NodeClass (where
	// ObservedGeneration is still 0).
	if nc.Status.LocalDNSState != nil &&
		(nc.Status.LocalDNSStateObservedGeneration != nc.Generation ||
			nc.Status.LocalDNSStateObservedKubernetesVersion != kv) {
		nc.Status.LocalDNSResolveFailures = 0
	}

	// Sticky-Enabled: once Enabled under Preferred, never auto-flip to Disabled.
	// Also no-op if already evaluated for this (spec gen, k8s version) tuple.
	if r.shortCircuitPreferred(nc, kv) {
		return reconcile.Result{}, nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, r.resolveTimeout)
	defer cancel()
	state, transientErr := r.resolvePreferred(resolveCtx, nc, kv)

	if transientErr != nil {
		// Forbidden is not transient — RBAC won't fix itself on retry. Fail-safe
		// to Disabled immediately so we don't burn the budget and delay
		// provisioning by maxFailures*backoff.
		if k8serrors.IsForbidden(transientErr) {
			log.FromContext(ctx).Info("localdns resolve denied by RBAC, defaulting to Disabled",
				"error", transientErr.Error())
			state = v1beta1.LocalDNSStateDisabled
			// fall through to commit
		} else {
			nc.Status.LocalDNSResolveFailures++
			if nc.Status.LocalDNSResolveFailures < r.maxFailures {
				// Don't commit state; stay False with a clear reason and retry.
				nc.StatusConditions().SetFalse(
					v1beta1.ConditionTypeLocalDNSReady,
					"ResolveTransientError",
					fmt.Sprintf("attempt %d/%d: %s", nc.Status.LocalDNSResolveFailures, r.maxFailures, transientErr),
				)
				return reconcile.Result{RequeueAfter: r.failureBackoff}, nil
			}
			// Budget exhausted — fail-safe to Disabled and unblock provisioning.
			log.FromContext(ctx).Info("localdns resolve failed too many times, defaulting to Disabled",
				"failures", nc.Status.LocalDNSResolveFailures, "error", transientErr.Error())
			state = v1beta1.LocalDNSStateDisabled
			// fall through to commit
		}
	}

	nc.Status.LocalDNSState = lo.ToPtr(state)
	nc.Status.LocalDNSStateObservedGeneration = nc.Generation
	nc.Status.LocalDNSStateObservedKubernetesVersion = kv
	nc.Status.LocalDNSResolveFailures = 0
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
	return reconcile.Result{}, nil
}

// handleSimpleModes covers the cases that don't require evaluating cluster
// state: Mode unset, Required, Disabled, or unknown/invalid. Returns done=true
// when the caller should return the supplied result.
func (r *LocalDNSReconciler) handleSimpleModes(nc *v1beta1.AKSNodeClass) bool {
	// Mode unset → no LocalDNS configuration; clear status fields and mark Ready.
	if nc.Spec.LocalDNS == nil || nc.Spec.LocalDNS.Mode == "" {
		nc.Status.LocalDNSState = nil
		nc.Status.LocalDNSStateObservedGeneration = 0
		nc.Status.LocalDNSStateObservedKubernetesVersion = ""
		nc.Status.LocalDNSResolveFailures = 0
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return true
	}

	switch nc.Spec.LocalDNS.Mode {
	case v1beta1.LocalDNSModeRequired:
		s := v1beta1.LocalDNSStateEnabled
		nc.Status.LocalDNSState = &s
		nc.Status.LocalDNSStateObservedGeneration = nc.Generation
		nc.Status.LocalDNSStateObservedKubernetesVersion = ""
		nc.Status.LocalDNSResolveFailures = 0
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return true
	case v1beta1.LocalDNSModeDisabled:
		s := v1beta1.LocalDNSStateDisabled
		nc.Status.LocalDNSState = &s
		nc.Status.LocalDNSStateObservedGeneration = nc.Generation
		nc.Status.LocalDNSStateObservedKubernetesVersion = ""
		nc.Status.LocalDNSResolveFailures = 0
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return true
	case v1beta1.LocalDNSModePreferred:
		return false
	default:
		// Unknown/invalid mode: clear any prior state so consumers don't act on
		// stale Enabled/Disabled values, but don't block provisioning. Validation
		// logic on the spec catches the invalid mode elsewhere.
		nc.Status.LocalDNSState = nil
		nc.Status.LocalDNSStateObservedGeneration = 0
		nc.Status.LocalDNSStateObservedKubernetesVersion = ""
		nc.Status.LocalDNSResolveFailures = 0
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return true
	}
}

// shortCircuitPreferred handles the two early-exit cases under Preferred mode:
// sticky Enabled, and same-tuple no-op. Returns true when the caller should
// stop reconciling.
func (r *LocalDNSReconciler) shortCircuitPreferred(nc *v1beta1.AKSNodeClass, kv string) bool {
	// Sticky-Enabled: once the resolved state is Enabled, never auto-flip to
	// Disabled. Skip the status write when we've already observed this
	// (gen, kv) tuple so we don't churn the API server on every reconcile.
	if nc.Status.LocalDNSState != nil && *nc.Status.LocalDNSState == v1beta1.LocalDNSStateEnabled {
		if nc.Status.LocalDNSStateObservedGeneration == nc.Generation &&
			nc.Status.LocalDNSStateObservedKubernetesVersion == kv {
			nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
			return true
		}
		nc.Status.LocalDNSStateObservedGeneration = nc.Generation
		nc.Status.LocalDNSStateObservedKubernetesVersion = kv
		nc.Status.LocalDNSResolveFailures = 0
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return true
	}
	// No-op if already evaluated for this (spec gen, k8s version) tuple.
	if nc.Status.LocalDNSState != nil &&
		nc.Status.LocalDNSStateObservedGeneration == nc.Generation &&
		nc.Status.LocalDNSStateObservedKubernetesVersion == kv {
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return true
	}
	return false
}

// resolvePreferred runs the Preferred-mode safety checks and returns the state
// to commit. It returns a non-nil error only for transient kube-API failures;
// definitive "no policies present" outcomes return (Enabled, nil) and
// definitive "should be disabled" outcomes return (Disabled, nil).
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
//  3. This function — sets Status.LocalDNSState for instance-type filtering /
//     cache key / node label.
//
// This function intentionally does NOT replicate every gate; the gaps are
// handled in different places:
//   - K8s ver: policy-locked (release-noted, immutable). Check 1 below is a
//     local optimization to avoid noisy reconciles below the threshold.
//   - SKU CPU/mem: handled at NodeClaim time — when LocalDNS is Enabled on the
//     NodeClass, only supported VM SKUs/sizes are selected, so the reconciler
//     does not need to gate on SKU here.
//   - IsLocalDNSSupported: only IsUbuntu2004 is reachable in NAP; the other
//     sub-checks (Windows, AvailabilitySets, CustomImage) are N/A in NAP.
//     Covered by Check 3 via the ResolvesToUbuntu2004 helper.
//   - BYO CNI / NetworkPolicy / node-local-dns DS: covered by Checks 2/4/5.
//
// Both karpenter provisioning paths land at the RP validator: getNodeBootstrapping
// → nodeprovisioner → CRP → RP validator; AKS Machine API
// (MachinesClient.BeginCreateOrUpdate) → validation/impl.go → localdns.NewValidator
// → RP validator. Karpenter sends only Mode+overrides on both wires.
func (r *LocalDNSReconciler) resolvePreferred(ctx context.Context, nc *v1beta1.AKSNodeClass, k8sVersion string) (v1beta1.LocalDNSState, error) {
	// Check 1: k8s version >= threshold.
	parsed, err := semver.ParseTolerant(strings.TrimPrefix(k8sVersion, "v"))
	if err != nil {
		// Cannot parse status k8s version — treat as not-yet-eligible (Disabled).
		return v1beta1.LocalDNSStateDisabled, nil //nolint:nilerr // intentional: malformed version is treated as not-eligible
	}
	if parsed.LT(r.versionThreshold) {
		return v1beta1.LocalDNSStateDisabled, nil
	}

	// Check 2: BYO CNI clusters are not supported for auto-enabling.
	if strings.EqualFold(r.networkPlugin, consts.NetworkPluginNone) {
		return v1beta1.LocalDNSStateDisabled, nil
	}

	// Check 3: image-family must support LocalDNS. LocalDNS is unsupported on
	// Ubuntu 20.04, which the resolver picks for the legacy/unset Ubuntu family
	// when FIPS mode is enabled (see imagefamily.defaultUbuntu). Mirror the
	// per-AP gate that nodeprovisioner runs server-side
	// (resourceprovider/sharedlib/containerservice/localdns/localdnshelper.go
	// IsLocalDNSSupported → IsUbuntu2004) so that the NodeClass status agrees
	// with the bootstrap-time decision and we don't reserve LocalDNS SKU
	// headroom / mislabel nodes for a NodeClass that will never get LocalDNS.
	if imagefamily.ResolvesToUbuntu2004(nc.Spec.ImageFamily, nc.Spec.FIPSMode) {
		return v1beta1.LocalDNSStateDisabled, nil
	}

	// Check 4: cilium/calico network policy → must have no conflicting policies.
	if strings.EqualFold(r.networkPolicy, networkPolicyCilium) || strings.EqualFold(r.networkPolicy, networkPolicyCalico) {
		conflict, err := r.hasConflictingNetworkPolicies(ctx, r.networkPolicy)
		if err != nil {
			return "", err
		}
		if conflict {
			return v1beta1.LocalDNSStateDisabled, nil
		}
	}

	// Check 5: upstream node-local-dns DaemonSet must not be present.
	has, err := r.hasUpstreamNodeLocalDNS(ctx)
	if err != nil {
		return "", err
	}
	if has {
		return v1beta1.LocalDNSStateDisabled, nil
	}

	return v1beta1.LocalDNSStateEnabled, nil
}

// hasUpstreamNodeLocalDNS returns true if a node-local-dns DaemonSet is present
// in kube-system. Returns a transient error on kube-API failure so the caller
// can decide whether to retry or fail-safe.
func (r *LocalDNSReconciler) hasUpstreamNodeLocalDNS(ctx context.Context) (bool, error) {
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

// hasConflictingNetworkPolicies returns true if the cluster has any
// NetworkPolicy / Cilium / Calico custom policies that would conflict with
// LocalDNS being enabled. The default kube-system/konnectivity-agent K8s
// NetworkPolicy is excluded.
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

func (r *LocalDNSReconciler) hasConflictingCRDNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
	if r.dynamicClient == nil {
		// No dynamic client wired (test scenarios); treat as no CRD policies.
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
				// Dynamic client returns NotFound when the CRD itself is not
				// installed on the cluster. (For a list call against a registered
				// CRD with zero items the server returns an empty list, not
				// NotFound, so this branch reliably means "kind unknown".)
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
