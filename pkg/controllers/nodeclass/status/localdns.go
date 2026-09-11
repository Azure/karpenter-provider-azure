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
	"sort"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/nodeoverlay"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

// localDNSPreferredVersionThreshold is the minimum Kubernetes version required
// for LocalDNS to be enabled under Mode=Preferred.
var localDNSPreferredVersionThreshold = lo.Must(semver.ParseTolerant(localDNSPreferredK8sVersionThreshold))

const (
	// localDNSPreferredK8sVersionThreshold is the minimum Kubernetes version
	// required to auto-enable LocalDNS when Spec.LocalDNS.Mode=Preferred.
	localDNSPreferredK8sVersionThreshold = "1.99.0"

	// konnectivityAgentPolicy{Name,Namespace} identify the AKS-managed
	// NetworkPolicy that is allow-listed when scanning for conflicting
	// NetworkPolicies during LocalDNS gate evaluation.
	konnectivityAgentPolicyName      = "konnectivity-agent"
	konnectivityAgentPolicyNamespace = "kube-system"

	// nodeLocalDNSDaemonSet{Name,Namespace} identify the upstream
	// node-local-dns DaemonSet whose presence disables LocalDNS in Preferred
	// mode.
	nodeLocalDNSDaemonSetName      = "node-local-dns"
	nodeLocalDNSDaemonSetNamespace = "kube-system"

	// reasonNoCompatibleInstanceTypes is set on LocalDNSReady when Preferred
	// resolves to Disabled because enabling LocalDNS would leave a referencing
	// NodePool with no provisionable instance type.
	reasonNoCompatibleInstanceTypes = "NoCompatibleInstanceTypes"

	// reasonNoReferencingNodePools is set on LocalDNSReady when Preferred
	// resolves to Disabled because no NodePool references the AKSNodeClass yet,
	// leaving the instance type gate nothing to evaluate against.
	reasonNoReferencingNodePools = "NoReferencingNodePools"

	// aksNodeClassKind is the NodeClassRef.Kind identifying an AKSNodeClass.
	aksNodeClassKind = "AKSNodeClass"
)

// localDNSPreferredRequeueAfter bounds how long the controller waits before
// re-evaluating Preferred-mode gates when none of the inputs change. Cluster
// gate inputs (k8s NetworkPolicies, upstream node-local-dns DS) can be
// mutated out-of-band without producing an AKSNodeClass event, so we requeue
// periodically.
const localDNSPreferredRequeueAfter = 5 * time.Minute

// LocalDNSReconciler resolves the effective LocalDNS state on an AKSNodeClass
// and stores it on Status.LocalDNSState.
//
// The Preferred-mode gates exist to protect one specific event: crossing the
// k8s version threshold turns LocalDNS on automatically, without the user
// asking for it. Anything that breaks as a result of that is AKS's doing, so
// before enabling we verify the cluster still works -- including, per the gate
// this reconciler adds, that every referencing NodePool can still provision a
// node. A cluster that could provision before the upgrade must be able to
// provision after it.
//
// That ownership ends once LocalDNS is on and healthy. A user who then adds a
// conflicting NetworkPolicy or narrows a NodePool below the LocalDNS VM size
// floor owns that outcome, which is why the decision is sticky.
//
// Behavior:
//   - Mode unset/nil  -> Status=Disabled, LocalDNSReady=True.
//   - Mode=Required   -> Status=Enabled, LocalDNSReady=True.
//   - Mode=Disabled   -> Status=Disabled, LocalDNSReady=True.
//   - Mode=Preferred  -> evaluate six gates (k8s>=1.36, !BYO CNI,
//     !ResolvesToUbuntu2004, no conflicting NetworkPolicies, no upstream
//     node-local-dns DS, and at least one NodePool references this NodeClass
//     with every such NodePool retaining a LocalDNS-compatible instance type)
//     and commit Enabled or Disabled. Once Enabled is committed it is sticky.
//
// Every Preferred outcome keeps LocalDNSReady=True. LocalDNSReady is one of the
// AKSNodeClass readiness conditions, so setting it False stops the NodeClass
// provisioning nodes at all -- which is the very breakage these gates exist to
// prevent. An undecided or negative verdict leaves LocalDNS off, which is
// precisely how the cluster behaved before the upgrade. Only a genuine error
// reaching the gate inputs reports False, and that is a real inability to
// judge, not a verdict.
type LocalDNSReconciler struct {
	kubeClient    kubernetes.Interface
	dynamicClient dynamic.Interface
	// crClient reads NodePools from the managed cluster. Distinct from
	// kubeClient, which is a client-go interface for core/apps resources.
	crClient             client.Client
	instanceTypeProvider instancetype.Provider
	// instanceTypeStore holds the NodeOverlay-adjusted view of instance types.
	// The gate has to see what the scheduler sees, so it applies the same
	// overlays the cloud provider does before judging a NodePool starved.
	instanceTypeStore *nodeoverlay.InstanceTypeStore
	networkPolicy     string
	networkPlugin     string
}

// NewLocalDNSReconciler constructs a LocalDNSReconciler.
func NewLocalDNSReconciler(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	crClient client.Client,
	instanceTypeProvider instancetype.Provider,
	instanceTypeStore *nodeoverlay.InstanceTypeStore,
	networkPolicy, networkPlugin string,
) *LocalDNSReconciler {
	return &LocalDNSReconciler{
		kubeClient:           kubeClient,
		dynamicClient:        dynamicClient,
		crClient:             crClient,
		instanceTypeProvider: instanceTypeProvider,
		instanceTypeStore:    instanceTypeStore,
		networkPolicy:        networkPolicy,
		networkPlugin:        networkPlugin,
	}
}

// Reconcile runs LocalDNS state resolution. It is invoked from the parent
// nodeclass.status Controller, which owns the Status patch.
func (r *LocalDNSReconciler) Reconcile(ctx context.Context, nc *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName("nodeclass.localdns"))

	// Mode unset -> Disabled, mark Ready.
	if nc.Spec.LocalDNS == nil || nc.Spec.LocalDNS.Mode == "" {
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
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
		return r.reconcilePreferred(ctx, nc)

	default:
		// Unknown mode: treat as Disabled and mark Ready -- spec validation surfaces
		// the bad value to the user elsewhere. CRD enum validation
		// (Required|Preferred|Disabled) should make this branch unreachable; log
		// at Error so we notice if an out-of-band CRD/spec mismatch ever lands here.
		log.FromContext(ctx).Error(nil, "unknown LocalDNS mode, defaulting to Disabled (unreachable: CRD enum validation should prevent this)", "mode", string(nc.Spec.LocalDNS.Mode))
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}
}

// reconcilePreferred resolves Mode=Preferred against the sticky-Enabled rule
// and the static + cluster gates. Split out of Reconcile to keep its
// cyclomatic complexity below the lint threshold.
func (r *LocalDNSReconciler) reconcilePreferred(ctx context.Context, nc *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	// Sticky-Enabled: once LocalDNS is on and the cluster is working, it stays
	// on. The gates below guard AKS's automatic enable, not the user's later
	// edits -- a user who adds a conflicting NetworkPolicy or narrows a NodePool
	// below the LocalDNS VM size floor after the fact owns that outcome.
	// Re-running the gates here would mean flipping LocalDNS back off, and
	// on/off churn reimages nodes: a far heavier interruption than whatever
	// condition it would be reacting to.
	if nc.Status.LocalDNSState != nil && *nc.Status.LocalDNSState == v1beta1.LocalDNSStateEnabled {
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	// Static gates first (no kube-API calls).
	staticOK, err := r.meetsStaticRequirements(nc)
	if err != nil {
		log.FromContext(ctx).V(1).Info("localdns resolve: static check error, requeuing", "error", err.Error())
		nc.StatusConditions().SetFalse(v1beta1.ConditionTypeLocalDNSReady, "CheckingClusterRequirementsFailed", err.Error())
		return reconcile.Result{}, err
	}
	if !staticOK {
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{RequeueAfter: localDNSPreferredRequeueAfter}, nil
	}

	// Cluster gates: any transient error -> return error so controller-runtime
	// requeues with backoff. Don't mark Ready=True.
	ok, err := r.meetsClusterRequirements(ctx)
	if err != nil {
		log.FromContext(ctx).V(1).Info("localdns resolve: transient error, requeuing", "error", err.Error())
		nc.StatusConditions().SetFalse(v1beta1.ConditionTypeLocalDNSReady, "CheckingClusterRequirementsFailed", err.Error())
		return reconcile.Result{}, err
	}
	if !ok {
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
		nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{RequeueAfter: localDNSPreferredRequeueAfter}, nil
	}

	// Instance-type gate last: it is the only gate that costs a provider call.
	reason, msg, err := r.checkInstanceTypeGate(ctx, nc)
	if err != nil {
		log.FromContext(ctx).V(1).Info("localdns resolve: instance type check error, requeuing", "error", err.Error())
		nc.StatusConditions().SetFalse(v1beta1.ConditionTypeLocalDNSReady, "CheckingClusterRequirementsFailed", err.Error())
		return reconcile.Result{}, err
	}
	if reason != "" {
		log.FromContext(ctx).Info("localdns resolve: disabling", "reason", reason, "message", msg)
		nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
		// Ready=True even for NoReferencingNodePools, where the gate had nothing
		// to evaluate. LocalDNSReady is an AKSNodeClass readiness condition, so
		// reporting False would block the NodeClass from provisioning anything
		// -- AKS breaking a working cluster over a LocalDNS decision the user
		// never asked for, which is the exact failure these gates exist to
		// prevent. Leaving LocalDNS off is instead how the cluster already
		// behaved before the upgrade made it eligible.
		nc.StatusConditions().SetTrueWithReason(v1beta1.ConditionTypeLocalDNSReady, reason, msg)
		return reconcile.Result{RequeueAfter: localDNSPreferredRequeueAfter}, nil
	}

	// Every gate passed, so commit immediately. Enabling is a one-way door --
	// the sticky rule above means it is never revisited -- which argues for
	// caution, but a settling delay before committing buys nothing here. The
	// instance type list is invalidated by a generation stamp over the two
	// inputs that can withdraw eligible VM sizes (unavailable offerings and
	// quota), so a stale optimistic view of those is not reachable. What remains
	// unsynchronized is the NodeOverlay store, and an overlay that strips a
	// NodePool's eligible sizes is a user edit -- already the user's own
	// responsibility under the sticky rule, not something AKS should stall an
	// automatic enable over.
	//
	// An under-populated read fails the other way: it yields no compatible
	// instance types, which resolves to Disabled, which is not sticky and is
	// re-decided on the next requeue.
	log.FromContext(ctx).Info("localdns resolve: enabling")
	nc.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
	return reconcile.Result{RequeueAfter: localDNSPreferredRequeueAfter}, nil
}

func (r *LocalDNSReconciler) meetsStaticRequirements(nc *v1beta1.AKSNodeClass) (bool, error) {
	k8sVersion, err := nc.GetKubernetesVersion()
	if err != nil {
		return false, fmt.Errorf("getting kubernetes version: %w", err)
	}
	if k8sVersion == "" {
		return false, nil
	}
	parsed, err := semver.ParseTolerant(strings.TrimPrefix(k8sVersion, "v"))
	if err != nil {
		return false, fmt.Errorf("parsing kubernetes version %q: %w", k8sVersion, err)
	}
	if parsed.LT(localDNSPreferredVersionThreshold) {
		return false, nil
	}
	if strings.EqualFold(r.networkPlugin, consts.NetworkPluginNone) {
		return false, nil
	}
	if imagefamily.ResolvesToUbuntu2004(nc.Spec.ImageFamily, nc.Spec.FIPSMode, nc.IsTrustedLaunchEnabled(), k8sVersion) {
		return false, nil
	}
	return true, nil
}

// meetsClusterRequirements returns true if cluster-side checks (network policies,
// node-local-dns DS) all pass. Errors are propagated to the caller.
func (r *LocalDNSReconciler) meetsClusterRequirements(ctx context.Context) (bool, error) {
	if strings.EqualFold(r.networkPolicy, consts.NetworkPolicyCilium) || strings.EqualFold(r.networkPolicy, consts.NetworkPolicyCalico) {
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

// checkInstanceTypeGate reports whether enabling LocalDNS is safe for every
// NodePool referencing nc. It returns an empty reason when the gate passes, or
// a LocalDNSReady reason plus an operator-facing message when it does not.
//
// Enabling LocalDNS activates isInstanceTypeSupportedByLocalDNS in the instance
// type provider, which drops every VM size below the LocalDNS resource floor. A
// NodePool whose requirements only admit such sizes would silently lose all of
// its candidates, leaving pods Pending behind core's generic "no instance type
// satisfied requirements". Rather than restating the floor here -- where it
// could drift from the provider's copy -- we ask the provider for the list it
// would produce with LocalDNS on.
func (r *LocalDNSReconciler) checkInstanceTypeGate(ctx context.Context, nc *v1beta1.AKSNodeClass) (reason, message string, err error) {
	nodePoolList := &karpv1.NodePoolList{}
	if err := r.crClient.List(ctx, nodePoolList); err != nil {
		return "", "", fmt.Errorf("listing NodePools: %w", err)
	}
	nodePools := lo.Filter(nodePoolList.Items, func(np karpv1.NodePool, _ int) bool {
		return referencesNodeClass(np, nc)
	})
	// Nothing to evaluate against. Committing Enabled here would be sticky, so
	// defer until a NodePool appears instead of locking LocalDNS on blind.
	if len(nodePools) == 0 {
		return reasonNoReferencingNodePools,
			fmt.Sprintf("no NodePool references AKSNodeClass %q yet, deferring LocalDNS until one does", nc.Name), nil
	}

	// Probe the provider as if LocalDNS were already Enabled: IsLocalDNSEnabled
	// reads Status.LocalDNSState, so this makes List apply the LocalDNS SKU
	// filter. LocalDNSEnabled is part of the provider's cache key, so this
	// result is cached separately from the LocalDNS-off list.
	probe := nc.DeepCopy()
	probe.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
	supported, err := r.instanceTypeProvider.List(ctx, probe)
	if err != nil {
		return "", "", fmt.Errorf("listing instance types: %w", err)
	}

	var starved []string
	for _, np := range nodePools {
		ok, err := r.canProvisionWithLocalDNS(ctx, np, supported)
		if err != nil {
			return "", "", err
		}
		if !ok {
			starved = append(starved, np.Name)
		}
	}
	if len(starved) > 0 {
		return reasonNoCompatibleInstanceTypes, fmt.Sprintf(
			"enabling LocalDNS would leave NodePool(s) %s with no provisionable instance type: no VM size meeting the LocalDNS resource floor satisfies their requirements, limits and minValues",
			summarizeNodePoolNames(starved)), nil
	}
	return "", "", nil
}

// canProvisionWithLocalDNS reports whether np could still launch a node if
// LocalDNS were enabled, given the LocalDNS-filtered instance type list. An
// error means the answer is not knowable right now and the caller should
// requeue rather than decide.
func (r *LocalDNSReconciler) canProvisionWithLocalDNS(ctx context.Context, np karpv1.NodePool, supported []*cloudprovider.InstanceType) (bool, error) {
	// Mirror how core builds a NodeClaim's requirements: spec.requirements
	// plus template.metadata.labels (see scheduling.NewNodeClaimTemplate).
	// A pool that pins node.kubernetes.io/instance-type through labels
	// rather than requirements is just as constrained, and reading only
	// spec.requirements would score it as unconstrained and pass the gate.
	reqs := scheduling.NewNodeSelectorRequirementsWithMinValues(np.Spec.Template.Spec.Requirements...)
	reqs.Add(scheduling.NewLabelRequirements(np.Spec.Template.Labels).Values()...)

	// NodeOverlays rewrite the instance type list per NodePool -- they can
	// add extended resources and drop types outright -- so the provider
	// list alone is not what the scheduler will see. Mirror
	// cloudprovider.resolveInstanceTypes: same feature gate, same
	// per-NodePool ApplyAll. An unevaluated NodePool yields
	// UnevaluatedNodePoolError; that pool cannot provision from any list
	// yet, so propagate it and re-decide on the requeue rather than
	// judging it against a stale view.
	candidates := supported
	if coreoptions.FromContext(ctx).FeatureGates.NodeOverlay && r.instanceTypeStore != nil {
		overlaid, err := r.instanceTypeStore.ApplyAll(np.Name, supported)
		if err != nil {
			return false, fmt.Errorf("applying NodeOverlays for NodePool %q: %w", np.Name, err)
		}
		candidates = overlaid
	}

	// Requirement compatibility only -- deliberately no
	// Offerings.Available() check, so a pool is not called starved because
	// its zone is momentarily out of capacity. This is not airtight: the
	// provider derives the zone, capacity-type, placement-scope and
	// UltraSSD requirements from available offerings, so a broad enough
	// outage can still empty those keys and narrow the match. The failure
	// is one-directional and self-healing -- LocalDNS stays Disabled until
	// the next requeue rather than flipping an Enabled NodeClass off --
	// which is why it is left to the periodic requeue instead of being
	// papered over with a second, drift-prone copy of provider logic here.
	compatible := cloudprovider.InstanceTypes(lo.Filter(candidates, func(it *cloudprovider.InstanceType, _ int) bool {
		return reqs.Compatible(it.Requirements, v1beta1.AllowUndefinedWellKnownAndRestrictedLabels) == nil
	}))
	compatible = withinNodePoolLimits(compatible, np.Spec.Limits)
	if len(compatible) == 0 {
		return false, nil
	}

	// A non-empty candidate set is necessary but not sufficient: core
	// applies minValues to the surviving set, not pairwise. A pool asking
	// for `instance-type In [Standard_B2s, Standard_D4s_v3]` with
	// minValues 2 has a compatible type here (D4s) yet cannot provision,
	// because the LocalDNS floor leaves only one distinct value. Under
	// BestEffort core relaxes minValues instead of failing, so honor the
	// configured policy rather than assuming Strict.
	//
	// SatisfiesMinValues' error is an answer, not a fault: it means "this
	// pool is starved", so it is folded into the bool rather than returned.
	if coreoptions.FromContext(ctx).MinValuesPolicy == coreoptions.MinValuesPolicyStrict {
		_, _, unsatisfied := compatible.SatisfiesMinValues(reqs)
		return unsatisfied == nil, nil
	}
	return true, nil
}

// maxNamedStarvedNodePools bounds how many NodePool names the LocalDNSReady
// message spells out. Condition messages are capped at 32768 characters by the
// CRD; a name can be 253, so listing every pool in a cluster with hundreds of
// them overruns the cap, the status patch is rejected, and the gate's decision
// is never persisted -- turning a cosmetic problem into a stuck reconcile. 25
// names is at most ~6.4KB, comfortably inside the cap, and the count in the
// suffix keeps the message honest about what was elided.
const maxNamedStarvedNodePools = 25

// summarizeNodePoolNames renders names for an operator-facing condition message:
// sorted (List order is not guaranteed, and an unstable message would rewrite
// the condition on every reconcile) and truncated to a bounded length.
func summarizeNodePoolNames(names []string) string {
	sort.Strings(names)
	if len(names) <= maxNamedStarvedNodePools {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (and %d more)",
		strings.Join(names[:maxNamedStarvedNodePools], ", "),
		len(names)-maxNamedStarvedNodePools)
}

// withinNodePoolLimits drops instance types that a NodePool's limits forbid
// outright, mirroring core's filterByRemainingResources. A single node of a
// type whose capacity exceeds the pool's whole limit can never be provisioned,
// so a LocalDNS floor that leaves only such types starves the pool just as
// surely as an empty requirement intersection.
//
// Core filters against limits *minus current usage*; this deliberately uses the
// raw limits, the most permissive static reading. Usage is transient, and
// folding it in would make LocalDNS enablement depend on how full the pool
// happens to be -- the same reason Offerings.Available() is skipped above.
func withinNodePoolLimits(its cloudprovider.InstanceTypes, limits karpv1.Limits) cloudprovider.InstanceTypes {
	if len(limits) == 0 {
		return its
	}
	return lo.Filter(its, func(it *cloudprovider.InstanceType, _ int) bool {
		for name, limit := range limits {
			if resources.Cmp(it.Capacity[name], limit) > 0 {
				return false
			}
		}
		return true
	})
}

// aksNodeClassRefName returns the name of the AKSNodeClass np references, and
// whether np references an AKSNodeClass at all. Group and Kind are matched
// alongside Name so that a NodePool pointing at some other provider's NodeClass
// that happens to share a name cannot spuriously disable LocalDNS.
//
// Both the instance type gate and the NodePool watch that feeds it resolve
// references through here, so the two cannot disagree about which NodePools
// belong to a NodeClass.
func aksNodeClassRefName(np *karpv1.NodePool) (string, bool) {
	ref := np.Spec.Template.Spec.NodeClassRef
	if ref == nil || ref.Kind != aksNodeClassKind || ref.Group != apis.Group {
		return "", false
	}
	return ref.Name, true
}

// referencesNodeClass reports whether np targets nc.
func referencesNodeClass(np karpv1.NodePool, nc *v1beta1.AKSNodeClass) bool {
	name, ok := aksNodeClassRefName(&np)
	return ok && name == nc.Name
}

func (r *LocalDNSReconciler) hasUpstreamNodeLocalDNS(ctx context.Context) (bool, error) {
	_, err := r.kubeClient.AppsV1().DaemonSets(nodeLocalDNSDaemonSetNamespace).Get(ctx, nodeLocalDNSDaemonSetName, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("checking node-local-dns daemonset: %w", err)
}

func (r *LocalDNSReconciler) hasConflictingNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
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
		if np.Name == konnectivityAgentPolicyName && np.Namespace == konnectivityAgentPolicyNamespace {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *LocalDNSReconciler) hasConflictingCRDNetworkPolicies(ctx context.Context, networkPolicyType string) (bool, error) {
	var crdResources []schema.GroupVersionResource
	switch {
	case strings.EqualFold(networkPolicyType, consts.NetworkPolicyCilium):
		crdResources = []schema.GroupVersionResource{
			{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"},
			{Group: "cilium.io", Version: "v2", Resource: "ciliumclusterwidenetworkpolicies"},
		}
	case strings.EqualFold(networkPolicyType, consts.NetworkPolicyCalico):
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
