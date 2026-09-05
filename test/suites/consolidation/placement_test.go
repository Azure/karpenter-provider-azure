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
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	podutils "sigs.k8s.io/karpenter/pkg/utils/pod"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// Match the common E2E eventual-assertion budget, but bound this placement phase
// and its separately supplied cleanup context rather than extending the It context.
const replacementBudgetPlacementTimeout = 16 * time.Minute

func replacementBudgetTaintExcludes(pool *karpv1.NodePool, pod *corev1.Pod) bool {
	var hardTaints scheduling.Taints
	for _, taint := range pool.Spec.Template.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			hardTaints = append(hardTaints, taint)
		}
	}
	return hardTaints.ToleratesPod(pod) != nil
}

func replacementBudgetPodExcluded(pool *karpv1.NodePool, pod *corev1.Pod) bool {
	return replacementBudgetTaintExcludes(pool, pod) || replacementBudgetHardExcludes(pool, &pod.Spec)
}

func replacementBudgetHardExcludes(pool *karpv1.NodePool, spec *corev1.PodSpec) bool {
	// These finite domains are upper bounds, not a guessed sample Node. Ignoring
	// other pool restrictions can miss a proof, but cannot invent one. In
	// particular, absent/unknown labels and provider defaults are not evidence.
	return replacementBudgetDomainsExclude(replacementBudgetLabelDomains(pool), spec)
}

func replacementBudgetDomainsExclude(domains map[string]sets.Set[string], spec *corev1.PodSpec) bool {
	for key, value := range spec.NodeSelector {
		if values, known := domains[key]; known && !values.Has(value) {
			return true
		}
	}
	if spec.Affinity == nil || spec.Affinity.NodeAffinity == nil {
		return false
	}
	required := spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if required == nil || len(required.NodeSelectorTerms) == 0 {
		return false
	}
	// Required terms are OR alternatives. Every term needs an independently
	// proved contradiction; preferred affinity and current nodeName never count.
	for _, term := range required.NodeSelectorTerms {
		if !replacementBudgetTermExcludes(domains, term) {
			return false
		}
	}
	return true
}

func replacementBudgetTermExcludes(domains map[string]sets.Set[string], term corev1.NodeSelectorTerm) bool {
	// Expressions (and MatchFields) are ANDed. Unknown fields cannot rescue a
	// known contradiction, but an unknown-only/empty term is not a proof.
	for _, expression := range term.MatchExpressions {
		if replacementBudgetExpressionExcludes(domains, expression) {
			return true
		}
	}
	return false
}

func replacementBudgetLabelDomains(pool *karpv1.NodePool) map[string]sets.Set[string] {
	domains := map[string]sets.Set[string]{}
	add := func(key string, values []string) bool {
		if _, err := labels.NewRequirement(key, selection.In, values); err != nil {
			return false
		}
		allowed := sets.New(values...)
		if previous, ok := domains[key]; ok {
			allowed = allowed.Intersection(previous)
		}
		if allowed.Len() == 0 {
			return false // Do not pass isolation by making the pool unsatisfiable.
		}
		domains[key] = allowed
		return true
	}
	for _, requirement := range pool.Spec.Template.Spec.Requirements {
		if requirement.Operator == corev1.NodeSelectorOpIn && !add(requirement.Key, requirement.Values) {
			return nil
		}
	}
	for key, value := range pool.Spec.Template.Labels {
		if !add(key, []string{value}) {
			return nil
		}
	}
	return domains
}

func replacementBudgetExpressionExcludes(domains map[string]sets.Set[string], expression corev1.NodeSelectorRequirement) bool {
	values, known := domains[expression.Key]
	if !known {
		return false
	}
	operators := map[corev1.NodeSelectorOperator]selection.Operator{
		corev1.NodeSelectorOpIn: selection.In, corev1.NodeSelectorOpNotIn: selection.NotIn,
		corev1.NodeSelectorOpExists: selection.Exists, corev1.NodeSelectorOpDoesNotExist: selection.DoesNotExist,
		corev1.NodeSelectorOpGt: selection.GreaterThan, corev1.NodeSelectorOpLt: selection.LessThan,
	}
	operator, supported := operators[expression.Operator]
	if !supported {
		return false
	}
	requirement, err := labels.NewRequirement(expression.Key, operator, expression.Values)
	if err != nil {
		return false
	}
	for value := range values {
		if requirement.Matches(labels.Set{expression.Key: value}) {
			return false
		}
	}
	return true
}

type replacementBudgetPlacement struct {
	kube         kubernetes.Interface // Always the uncached typed client in the live suite.
	pool         *karpv1.NodePool
	manager      string
	pollInterval time.Duration
	timeout      time.Duration
	started      bool
	active       bool
	targets      []*budgetPlacementTarget
}

type budgetPlacementApplyOutcome string

const (
	budgetPlacementApplyUnknown      budgetPlacementApplyOutcome = "unknown"
	budgetPlacementApplyAcknowledged budgetPlacementApplyOutcome = "acknowledged"
	budgetPlacementApplyRejected     budgetPlacementApplyOutcome = "rejected"
)

type budgetPlacementApply struct {
	uid                     types.UID
	resourceVersion         string
	install                 bool
	outcome                 budgetPlacementApplyOutcome
	fencedAtResourceVersion string // A same-UID current version that invalidates this exact request's old-RV CAS.
}

type budgetPlacementTarget struct {
	key                 types.NamespacedName
	uid                 types.UID
	writeSelector       bool
	attempted           bool // Set before the request: an error may leave a write pending or persisted.
	confirmed           bool // A fresh GET proved our exact selector intent was installed.
	released            bool // Intent is absent and every submitted apply is rejected or fenced.
	submittedApplies    []budgetPlacementApply
	baselinePodSelector map[string]string
	beforeActivation    sets.Set[types.UID]
	beforeRelease       sets.Set[types.UID]
	lastObservation     string
}

type budgetPlacementPod struct {
	pod        *corev1.Pod
	replicaSet *appsv1.ReplicaSet
}

type budgetPlacementOwner struct {
	manager   string
	operation metav1.ManagedFieldsOperationType
}

func newReplacementBudgetPlacement(kube kubernetes.Interface, pool *karpv1.NodePool) *replacementBudgetPlacement {
	scope := &replacementBudgetPlacement{
		kube: kube, manager: "consolidation-placement-" + uuid.NewString(),
		pollInterval: time.Second, timeout: replacementBudgetPlacementTimeout,
	}
	if pool != nil {
		scope.pool = pool.DeepCopy()
	}
	return scope
}

func (s *replacementBudgetPlacement) Activate(ctx context.Context, register func(func(context.Context) error)) error {
	if s.started || register == nil {
		return fmt.Errorf("placement activation requires one scope and a cleanup registrar")
	}
	s.started = true
	// Registration precedes discovery and every possible mutation, including a
	// write whose response is lost. The caller supplies the cleanup node's context.
	register(s.Restore)
	if err := s.validateInputs(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	targets, err := s.plan(ctx)
	if err != nil {
		return err
	}
	s.targets = targets // Retain every rollback target before writing any of them.
	for _, target := range s.targets {
		if err := s.activateTarget(ctx, target); err != nil {
			return err
		}
	}
	if err := s.waitForTargets(ctx, s.targets, false); err != nil {
		return err
	}
	if err := s.verifyUnrelated(ctx, nil); err != nil {
		return err
	}
	s.active = true
	return nil
}

func (s *replacementBudgetPlacement) validateInputs() error {
	if s.kube == nil || s.pool == nil || !replacementBudgetRequiresUser(s.pool) {
		return fmt.Errorf("placement requires an uncached client and an explicit user-only budget pool")
	}
	return nil
}

func (s *replacementBudgetPlacement) activateTarget(ctx context.Context, target *budgetPlacementTarget) error {
	if !target.writeSelector {
		return nil
	}
	if err := s.captureBaseline(ctx, target); err != nil {
		return err
	}
	return s.mutateSelector(ctx, target, true)
}

func replacementBudgetRequiresUser(pool *karpv1.NodePool) bool {
	found := false
	for _, requirement := range pool.Spec.Template.Spec.Requirements {
		if requirement.Key != v1beta1.AKSLabelMode {
			continue
		}
		if found || requirement.Operator != corev1.NodeSelectorOpIn || len(requirement.Values) != 1 || requirement.Values[0] != v1beta1.ModeUser {
			return false
		}
		found = true
	}
	values := replacementBudgetLabelDomains(pool)[v1beta1.AKSLabelMode]
	return found && values.Len() == 1 && values.Has(v1beta1.ModeUser)
}

func (s *replacementBudgetPlacement) plan(ctx context.Context) ([]*budgetPlacementTarget, error) {
	pods, err := s.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list unrelated pods before placement: %w", err)
	}
	byUID := map[types.UID]*budgetPlacementTarget{}
	for i := range pods.Items {
		target, err := s.planTarget(ctx, &pods.Items[i])
		if err != nil {
			return nil, err
		}
		if target == nil {
			continue
		}
		if previous, exists := byUID[target.uid]; exists {
			if previous.key != target.key {
				return nil, fmt.Errorf("deployment UID %s identifies both %s and %s", target.uid, previous.key, target.key)
			}
			continue
		}
		byUID[target.uid] = target
	}
	targets := make([]*budgetPlacementTarget, 0, len(byUID))
	for _, target := range byUID {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].key.String() < targets[j].key.String() })
	return targets, nil
}

func (s *replacementBudgetPlacement) planTarget(ctx context.Context, pod *corev1.Pod) (*budgetPlacementTarget, error) {
	if podutils.IsOwnedByDaemonSet(pod) || replacementBudgetTaintExcludes(s.pool, pod) {
		return nil, nil
	}
	if _, err := budgetPlacementController(pod, "ReplicaSet"); err != nil {
		if replacementBudgetHardExcludes(s.pool, &pod.Spec) {
			return nil, nil // A proved hard exclusion needs no unsupported-owner mutation.
		}
		return nil, fmt.Errorf("compatible pod %s/%s uid=%s selector=%v affinity=%v tolerations=%v: %w", pod.Namespace, pod.Name, pod.UID, pod.Spec.NodeSelector, pod.Spec.Affinity, pod.Spec.Tolerations, err)
	}
	deployment, err := s.resolveDeployment(ctx, pod)
	if err != nil {
		return nil, err
	}
	target := &budgetPlacementTarget{
		key: types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}, uid: deployment.UID,
		writeSelector: !replacementBudgetPodExcluded(s.pool, &corev1.Pod{Spec: deployment.Spec.Template.Spec}),
	}
	if target.writeSelector {
		if err := validateBudgetPlacementWritable(deployment); err != nil {
			return nil, fmt.Errorf("cannot isolate Deployment %s uid=%s: %w", target.key, target.uid, err)
		}
	}
	return target, nil
}

func budgetPlacementController(object metav1.Object, kind string) (*metav1.OwnerReference, error) {
	var controller *metav1.OwnerReference
	for _, owner := range object.GetOwnerReferences() {
		if owner.Controller == nil || !*owner.Controller {
			continue
		}
		if controller != nil {
			return nil, fmt.Errorf("multiple controlling owners for %s/%s", object.GetNamespace(), object.GetName())
		}
		controller = owner.DeepCopy()
	}
	if controller == nil || controller.APIVersion != "apps/v1" || controller.Kind != kind || controller.Name == "" || controller.UID == "" {
		return nil, fmt.Errorf("%s/%s requires an exact apps/v1 %s controller UID, got %v", object.GetNamespace(), object.GetName(), kind, controller)
	}
	return controller, nil
}

func budgetPlacementIdentity(object metav1.Object) error {
	if object.GetNamespace() == "" || object.GetName() == "" || object.GetUID() == "" || object.GetResourceVersion() == "" {
		return fmt.Errorf("incomplete object identity: %s/%s uid=%s rv=%s", object.GetNamespace(), object.GetName(), object.GetUID(), object.GetResourceVersion())
	}
	if object.GetDeletionTimestamp() != nil {
		return fmt.Errorf("object %s/%s uid=%s is deleting", object.GetNamespace(), object.GetName(), object.GetUID())
	}
	return nil
}

func (s *replacementBudgetPlacement) getDeployment(ctx context.Context, key types.NamespacedName, uid types.UID) (*appsv1.Deployment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key.Namespace == "" || key.Name == "" || uid == "" {
		return nil, fmt.Errorf("cannot read Deployment without its exact identity: %s uid=%s", key, uid)
	}
	deployment, err := s.kube.AppsV1().Deployments(key.Namespace).Get(ctx, key.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Deployment %s expected uid=%s: %w", key, uid, err)
	}
	if deployment == nil {
		return nil, fmt.Errorf("Deployment GET returned nil for %s", key)
	}
	if (types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}) != key {
		return nil, fmt.Errorf("Deployment GET did not return requested identity %s", key)
	}
	if err := budgetPlacementIdentity(deployment); err != nil {
		return nil, err
	}
	if deployment.UID != uid {
		return nil, fmt.Errorf("Deployment %s changed UID: expected %s, got %s", key, uid, deployment.UID)
	}
	return deployment, nil
}

func (s *replacementBudgetPlacement) resolvePod(ctx context.Context, listed *corev1.Pod) (*corev1.Pod, error) {
	if err := budgetPlacementIdentity(listed); err != nil {
		return nil, err
	}
	pod, err := s.kube.CoreV1().Pods(listed.Namespace).Get(ctx, listed.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Pod %s/%s expected uid=%s: %w", listed.Namespace, listed.Name, listed.UID, err)
	}
	if pod == nil || pod.Name != listed.Name || pod.Namespace != listed.Namespace {
		return nil, fmt.Errorf("Pod GET did not return requested identity %s/%s", listed.Namespace, listed.Name)
	}
	if pod.UID != listed.UID {
		return nil, fmt.Errorf("Pod %s/%s changed UID from %s to %s", pod.Namespace, pod.Name, listed.UID, pod.UID)
	}
	if err := budgetPlacementIdentity(pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func (s *replacementBudgetPlacement) resolveReplicaSet(ctx context.Context, pod *corev1.Pod) (*appsv1.ReplicaSet, error) {
	owner, err := budgetPlacementController(pod, "ReplicaSet")
	if err != nil {
		return nil, err
	}
	rs, err := s.kube.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("resolve Pod %s/%s uid=%s to ReplicaSet %s uid=%s: %w", pod.Namespace, pod.Name, pod.UID, owner.Name, owner.UID, err)
	}
	if rs == nil || rs.Name != owner.Name || rs.Namespace != pod.Namespace {
		return nil, fmt.Errorf("ReplicaSet GET did not return requested identity %s/%s", pod.Namespace, owner.Name)
	}
	if rs.UID != owner.UID {
		return nil, fmt.Errorf("ReplicaSet %s/%s changed UID: Pod references %s, got %s", rs.Namespace, rs.Name, owner.UID, rs.UID)
	}
	if err := budgetPlacementIdentity(rs); err != nil {
		return nil, err
	}
	if err := budgetPlacementSelectorMatches(rs.Spec.Selector, pod.Labels); err != nil {
		return nil, fmt.Errorf("Pod %s/%s does not match owning ReplicaSet %s: %w", pod.Namespace, pod.Name, rs.Name, err)
	}
	return rs, nil
}

func (s *replacementBudgetPlacement) resolveDeployment(ctx context.Context, listed *corev1.Pod) (*appsv1.Deployment, error) {
	pod, err := s.resolvePod(ctx, listed)
	if err != nil {
		return nil, err
	}
	rs, err := s.resolveReplicaSet(ctx, pod)
	if err != nil {
		return nil, err
	}
	controller, err := budgetPlacementController(rs, "Deployment")
	if err != nil {
		return nil, err
	}
	deployment, err := s.getDeployment(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: controller.Name}, controller.UID)
	if err != nil {
		return nil, err
	}
	if err := budgetPlacementSelectorMatches(deployment.Spec.Selector, rs.Spec.Template.Labels); err != nil {
		return nil, fmt.Errorf("ReplicaSet %s/%s does not match owning Deployment %s: %w", rs.Namespace, rs.Name, deployment.Name, err)
	}
	return deployment, nil
}

func budgetPlacementSelectorMatches(selector *metav1.LabelSelector, values map[string]string) error {
	if selector == nil || len(selector.MatchLabels)+len(selector.MatchExpressions) == 0 {
		return fmt.Errorf("missing owner selector")
	}
	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return err
	}
	if !parsed.Matches(labels.Set(values)) {
		return fmt.Errorf("labels %v do not match selector %v", values, selector)
	}
	return nil
}

func budgetPlacementManagedOwners(deployment *appsv1.Deployment, atomic bool, path ...string) ([]budgetPlacementOwner, error) {
	var owners []budgetPlacementOwner
	for _, entry := range deployment.ManagedFields {
		if entry.Subresource == "status" || entry.Subresource == "scale" {
			continue // Neither Deployment subresource can write the Pod template.
		}
		if err := validateBudgetPlacementFieldEntry(entry); err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(entry.FieldsV1.Raw, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("invalid managed fields for %q: %v", entry.Manager, err)
		}
		found, err := budgetPlacementFieldPath(fields, atomic, path)
		if err != nil {
			return nil, fmt.Errorf("managed fields for %q: %w", entry.Manager, err)
		}
		if found {
			owners = append(owners, budgetPlacementOwner{manager: entry.Manager, operation: entry.Operation})
		}
	}
	return owners, nil
}

func validateBudgetPlacementFieldEntry(entry metav1.ManagedFieldsEntry) error {
	if entry.Subresource != "" || entry.APIVersion != "apps/v1" || entry.FieldsType != "FieldsV1" || entry.FieldsV1 == nil || entry.Manager == "" {
		return fmt.Errorf("cannot verify managed fields for manager %q version=%q subresource=%q", entry.Manager, entry.APIVersion, entry.Subresource)
	}
	if entry.Operation != metav1.ManagedFieldsOperationApply && entry.Operation != metav1.ManagedFieldsOperationUpdate {
		return fmt.Errorf("unknown managed-field operation %q for %q", entry.Operation, entry.Manager)
	}
	return nil
}

func budgetPlacementFieldPath(fields map[string]json.RawMessage, atomic bool, path []string) (bool, error) {
	for i, key := range path {
		value, exists := fields[key]
		if !exists {
			return false, nil
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(value, &nested); err != nil || nested == nil {
			return false, fmt.Errorf("invalid managed-field path %v: %v", path[:i+1], err)
		}
		fields = nested
		if i == len(path)-1 {
			if atomic && len(fields) != 0 {
				return false, fmt.Errorf("nodeSelector ownership must be an atomic leaf")
			}
			return true, nil
		}
		if len(fields) == 0 {
			return true, nil // An ancestor-owned field is not permission to claim a child.
		}
	}
	return false, nil
}

func budgetPlacementSelectorOwners(deployment *appsv1.Deployment) ([]budgetPlacementOwner, error) {
	return budgetPlacementManagedOwners(deployment, true, "f:spec", "f:template", "f:spec", "f:nodeSelector")
}

func validateBudgetPlacementWritable(deployment *appsv1.Deployment) error {
	if err := validateBudgetPlacementRolloutTarget(deployment); err != nil {
		return err
	}
	owners, err := budgetPlacementSelectorOwners(deployment)
	if err != nil {
		return err
	}
	if deployment.Spec.Template.Spec.NodeSelector != nil || len(owners) != 0 {
		return fmt.Errorf("whole atomic nodeSelector must be absent and unowned, got selector=%v owners=%v", deployment.Spec.Template.Spec.NodeSelector, owners)
	}
	templateOwners, err := budgetPlacementManagedOwners(deployment, false, "f:spec", "f:template")
	if err != nil {
		return err
	}
	if len(templateOwners) == 0 {
		return fmt.Errorf("cannot verify an SSA owner for the existing Pod template")
	}
	for _, owner := range templateOwners {
		if owner.operation != metav1.ManagedFieldsOperationApply {
			return fmt.Errorf("template owner %q uses %s, not the required SSA omission contract", owner.manager, owner.operation)
		}
	}
	return nil
}

func validateBudgetPlacementRolloutTarget(deployment *appsv1.Deployment) error {
	if err := budgetPlacementIdentity(deployment); err != nil {
		return err
	}
	if deployment.Spec.Paused || deployment.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 1 {
		return fmt.Errorf("requires a non-paused RollingUpdate Deployment with desired replicas")
	}
	if _, labelled := deployment.Labels[coretest.DiscoveryLabel]; labelled {
		return fmt.Errorf("refusing a Deployment already marked for generic test cleanup")
	}
	if replacementBudgetDomainsExclude(map[string]sets.Set[string]{v1beta1.AKSLabelMode: sets.New(v1beta1.ModeSystem)}, &deployment.Spec.Template.Spec) {
		return fmt.Errorf("the owner template has hard constraints incompatible with system placement")
	}
	return nil
}

const budgetPlacementSelectorFields = `{"f:spec":{"f:template":{"f:spec":{"f:nodeSelector":{}}}}}`

func (s *replacementBudgetPlacement) validateOwned(deployment *appsv1.Deployment) error {
	owners, err := budgetPlacementSelectorOwners(deployment)
	if err != nil {
		return err
	}
	if !maps.Equal(deployment.Spec.Template.Spec.NodeSelector, map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}) || len(owners) != 1 || owners[0].manager != s.manager || owners[0].operation != metav1.ManagedFieldsOperationApply {
		return fmt.Errorf("placement intent changed or is not exclusively owned: Deployment %s/%s uid=%s rv=%s selector=%v owners=%v expected manager=%s", deployment.Namespace, deployment.Name, deployment.UID, deployment.ResourceVersion, deployment.Spec.Template.Spec.NodeSelector, owners, s.manager)
	}
	return s.validateManagerIntent(deployment)
}

func (s *replacementBudgetPlacement) validateManagerIntent(deployment *appsv1.Deployment) error {
	// Omitting an unexpectedly broadened field set could prune other properties.
	// Require this unique manager to own exactly the minimal intent we sent.
	var expected interface{}
	if err := json.Unmarshal([]byte(budgetPlacementSelectorFields), &expected); err != nil {
		return fmt.Errorf("decode selector field contract: %w", err)
	}
	for _, entry := range deployment.ManagedFields {
		if entry.Manager != s.manager {
			continue
		}
		var actual interface{}
		if entry.FieldsV1 == nil || json.Unmarshal(entry.FieldsV1.Raw, &actual) != nil || entry.Subresource != "" || !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("placement manager %q owns unexpected fields; will not omit its intent", s.manager)
		}
	}
	return nil
}

func (s *replacementBudgetPlacement) validateRestored(deployment *appsv1.Deployment) error {
	owners, err := budgetPlacementSelectorOwners(deployment)
	if err != nil {
		return err
	}
	if deployment.Spec.Template.Spec.NodeSelector != nil || len(owners) != 0 {
		return fmt.Errorf("selector was not restored to absent/unowned: Deployment %s/%s uid=%s selector=%v owners=%v", deployment.Namespace, deployment.Name, deployment.UID, deployment.Spec.Template.Spec.NodeSelector, owners)
	}
	for _, entry := range deployment.ManagedFields {
		if entry.Manager == s.manager {
			return fmt.Errorf("placement manager %q still owns unexpected intent after selector removal", s.manager)
		}
	}
	return nil
}

func budgetPlacementSelectorPatch(deployment *appsv1.Deployment, install bool) ([]byte, error) {
	if err := budgetPlacementIdentity(deployment); err != nil {
		return nil, err
	}
	intent := map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]interface{}{
			"name": deployment.Name, "namespace": deployment.Namespace,
			"uid": deployment.UID, "resourceVersion": deployment.ResourceVersion,
		},
	}
	if install {
		intent["spec"] = map[string]interface{}{"template": map[string]interface{}{"spec": map[string]interface{}{
			"nodeSelector": map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem},
		}}}
	}
	return json.Marshal(intent)
}

func validateBudgetPlacementResponse(before, after *appsv1.Deployment, install bool) error {
	if after == nil {
		return fmt.Errorf("SSA returned no Deployment")
	}
	if after.ResourceVersion == "" || after.ResourceVersion == before.ResourceVersion || after.Generation <= before.Generation {
		return fmt.Errorf("SSA response did not record a Pod-template change")
	}
	expected := before.DeepCopy()
	expected.Spec.Template.Spec.NodeSelector = nil
	if install {
		expected.Spec.Template.Spec.NodeSelector = map[string]string{v1beta1.AKSLabelMode: v1beta1.ModeSystem}
	}
	// This is comparison only, never a preimage sent back to the server. SSA is
	// allowed to change version, generation and managedFields, not other metadata.
	metadata := after.ObjectMeta.DeepCopy()
	metadata.ResourceVersion = before.ResourceVersion
	metadata.Generation = before.Generation
	metadata.ManagedFields = before.ManagedFields
	if !reflect.DeepEqual(after.Spec, expected.Spec) || !reflect.DeepEqual(metadata, &before.ObjectMeta) {
		return fmt.Errorf("SSA response changed fields outside the temporary selector intent or exact identity")
	}
	return nil
}

func (s *replacementBudgetPlacement) mutateSelector(ctx context.Context, target *budgetPlacementTarget, install bool) error {
	lastConflictRV := ""
	var lastConflict error
	for {
		before, err := s.prepareSelectorMutation(ctx, target, install)
		if err != nil {
			return err
		}
		if before.ResourceVersion == lastConflictRV {
			return fmt.Errorf("SSA conflict without a new resourceVersion for %s: %w", target.key, lastConflict)
		}
		after, err := s.writeSelector(ctx, target, before, install)
		if apierrors.IsConflict(err) {
			// Only retry after a new version AND all identity/ownership checks.
			// A field-manager conflict never grants permission to force or fight it.
			lastConflictRV, lastConflict = before.ResourceVersion, err
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.pollInterval):
				continue
			}
		}
		if err != nil {
			return fmt.Errorf("SSA selector mutation for %s install=%t: %w", target.key, install, err)
		}
		return s.verifyPersistedSelector(ctx, target, after, install)
	}
}

func (s *replacementBudgetPlacement) prepareSelectorMutation(ctx context.Context, target *budgetPlacementTarget, install bool) (*appsv1.Deployment, error) {
	before, err := s.getDeployment(ctx, target.key, target.uid)
	if err != nil {
		return nil, err
	}
	if install {
		err = validateBudgetPlacementWritable(before)
	} else {
		err = s.validateOwned(before)
	}
	if err != nil {
		return nil, fmt.Errorf("refuse selector mutation for %s: %w", target.key, err)
	}
	return before, nil
}

func (s *replacementBudgetPlacement) writeSelector(ctx context.Context, target *budgetPlacementTarget, before *appsv1.Deployment, install bool) (*appsv1.Deployment, error) {
	patch, err := budgetPlacementSelectorPatch(before, install)
	if err != nil {
		return nil, err
	}
	if install {
		target.attempted = true
	}
	// Retain every submitted precondition before invoking the client. A later
	// request's rejection cannot resolve an earlier unknown request at the same RV.
	submission := len(target.submittedApplies)
	target.submittedApplies = append(target.submittedApplies, budgetPlacementApply{
		uid: before.UID, resourceVersion: before.ResourceVersion, install: install, outcome: budgetPlacementApplyUnknown,
	})
	target.released = false
	after, err := s.kube.AppsV1().Deployments(target.key.Namespace).Patch(ctx, target.key.Name, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: s.manager})
	if err != nil {
		// Conflict conclusively rejects this request, matching mutateSelector's
		// existing retry contract. Other errors remain conservatively unknown;
		// cancellation or timeout can precede persistence rather than prevent it.
		if apierrors.IsConflict(err) {
			target.submittedApplies[submission].outcome = budgetPlacementApplyRejected
		}
		return nil, err
	}
	if err := validateBudgetPlacementResponse(before, after, install); err != nil {
		return nil, fmt.Errorf("verify SSA response for %s: %w", target.key, err)
	}
	// A valid reply still needs persisted readback. It cannot justify releasing
	// an absent selector observed at the unchanged submitted resourceVersion.
	target.submittedApplies[submission].outcome = budgetPlacementApplyAcknowledged
	return after, nil
}

func (s *replacementBudgetPlacement) verifyPersistedSelector(ctx context.Context, target *budgetPlacementTarget, response *appsv1.Deployment, install bool) error {
	// A successful Patch (including a fake/no-op response) is not evidence of
	// persisted intent. A new uncached GET must prove identity and ownership.
	current, err := s.getDeployment(ctx, target.key, target.uid)
	if err != nil {
		return err
	}
	if current.Generation < response.Generation {
		return fmt.Errorf("persisted Deployment %s generation %d predates SSA response generation %d", target.key, current.Generation, response.Generation)
	}
	if install {
		if err := s.validateOwned(current); err != nil {
			return err
		}
		target.confirmed = true
	} else {
		if err := s.validateRestored(current); err != nil {
			return err
		}
		if err := target.resolveSubmittedApplies(current); err != nil {
			return err
		}
		target.released = true
	}
	return nil
}

func (s *replacementBudgetPlacement) ownedPods(ctx context.Context, deployment *appsv1.Deployment) ([]budgetPlacementPod, error) {
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil || deployment.Spec.Selector == nil || selector.Empty() {
		return nil, fmt.Errorf("invalid Deployment selector for %s/%s: %v", deployment.Namespace, deployment.Name, err)
	}
	owned, err := s.ownedReplicaSets(ctx, deployment)
	if err != nil {
		return nil, err
	}
	pods, err := s.kube.CoreV1().Pods(deployment.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Pods for %s/%s: %w", deployment.Namespace, deployment.Name, err)
	}
	return budgetPlacementAssociatePods(deployment, selector, owned, pods.Items)
}

func (s *replacementBudgetPlacement) ownedReplicaSets(ctx context.Context, deployment *appsv1.Deployment) (map[types.UID]*appsv1.ReplicaSet, error) {
	replicaSets, err := s.kube.AppsV1().ReplicaSets(deployment.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list ReplicaSets for %s/%s: %w", deployment.Namespace, deployment.Name, err)
	}
	owned := map[types.UID]*appsv1.ReplicaSet{}
	for i := range replicaSets.Items {
		rs := &replicaSets.Items[i]
		owner, err := budgetPlacementController(rs, "Deployment")
		if err != nil || owner.UID != deployment.UID {
			continue
		}
		if owner.Name != deployment.Name || rs.UID == "" || rs.ResourceVersion == "" || rs.Namespace != deployment.Namespace {
			return nil, fmt.Errorf("invalid ReplicaSet identity for Deployment %s/%s uid=%s", deployment.Namespace, deployment.Name, deployment.UID)
		}
		owned[rs.UID] = rs
	}
	return owned, nil
}

func budgetPlacementAssociatePods(deployment *appsv1.Deployment, selector labels.Selector, owned map[types.UID]*appsv1.ReplicaSet, pods []corev1.Pod) ([]budgetPlacementPod, error) {
	var related []budgetPlacementPod
	for i := range pods {
		pod := &pods[i]
		owner, err := budgetPlacementController(pod, "ReplicaSet")
		if err != nil {
			if selector.Matches(labels.Set(pod.Labels)) {
				return nil, err // Matching labels cannot substitute for an exact owner chain.
			}
			continue
		}
		rs, exists := owned[owner.UID]
		if !exists {
			if selector.Matches(labels.Set(pod.Labels)) {
				return nil, fmt.Errorf("Pod %s/%s uid=%s has no verified ReplicaSet UID chain to Deployment uid=%s", pod.Namespace, pod.Name, pod.UID, deployment.UID)
			}
			continue
		}
		if err := validateBudgetPlacementPodOwner(pod, rs, owner); err != nil {
			return nil, err
		}
		related = append(related, budgetPlacementPod{pod: pod, replicaSet: rs})
	}
	return related, nil
}

func validateBudgetPlacementPodOwner(pod *corev1.Pod, rs *appsv1.ReplicaSet, owner *metav1.OwnerReference) error {
	if owner.Name != rs.Name || pod.UID == "" || pod.ResourceVersion == "" || pod.Namespace != rs.Namespace {
		return fmt.Errorf("Pod %s/%s has inconsistent ReplicaSet identity", pod.Namespace, pod.Name)
	}
	return budgetPlacementSelectorMatches(rs.Spec.Selector, pod.Labels)
}

func (s *replacementBudgetPlacement) captureBaseline(ctx context.Context, target *budgetPlacementTarget) error {
	deployment, err := s.getDeployment(ctx, target.key, target.uid)
	if err != nil {
		return err
	}
	pods, err := s.rolloutReady(ctx, deployment, target, "baseline")
	if err != nil {
		return fmt.Errorf("refuse to alter an unhealthy Deployment %s: %w", target.key, err)
	}
	target.beforeActivation = sets.New[types.UID]()
	for i, related := range pods {
		if !maps.Equal(related.pod.Spec.NodeSelector, deployment.Spec.Template.Spec.NodeSelector) {
			return fmt.Errorf("Deployment %s has a Pod selector different from its template; restoration of that placement is not proven", target.key)
		}
		if i == 0 {
			target.baselinePodSelector = maps.Clone(related.pod.Spec.NodeSelector)
		} else if !maps.Equal(target.baselinePodSelector, related.pod.Spec.NodeSelector) {
			return fmt.Errorf("Deployment %s has ambiguous baseline Pod selectors", target.key)
		}
		target.beforeActivation.Insert(related.pod.UID)
	}
	return nil
}

func (s *replacementBudgetPlacement) rolloutReady(ctx context.Context, deployment *appsv1.Deployment, target *budgetPlacementTarget, phase string) ([]budgetPlacementPod, error) {
	if deployment.Spec.Paused || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 1 {
		return nil, fmt.Errorf("Deployment %s is paused or has no desired replicas", target.key)
	}
	desired := *deployment.Spec.Replicas
	status := deployment.Status
	if !budgetPlacementDeploymentAvailable(deployment, desired) {
		return nil, fmt.Errorf("Deployment %s uid=%s generation=%d desired=%d rollout status=%+v", target.key, target.uid, deployment.Generation, desired, status)
	}
	pods, err := s.ownedPods(ctx, deployment)
	if err != nil {
		return nil, err
	}
	if len(pods) != int(desired) {
		return nil, fmt.Errorf("Deployment %s has %d actual Pods (including old/terminating), want %d", target.key, len(pods), desired)
	}
	for _, related := range pods {
		if err := s.verifyRolloutPod(ctx, deployment, target, related, phase); err != nil {
			return nil, err
		}
	}
	return pods, nil
}

func budgetPlacementDeploymentAvailable(deployment *appsv1.Deployment, desired int32) bool {
	status := deployment.Status
	return status.ObservedGeneration >= deployment.Generation && status.UpdatedReplicas == desired && status.ReadyReplicas == desired && status.AvailableReplicas == desired && status.Replicas == desired && status.UnavailableReplicas == 0
}

func (s *replacementBudgetPlacement) verifyRolloutPod(ctx context.Context, deployment *appsv1.Deployment, target *budgetPlacementTarget, related budgetPlacementPod, phase string) error {
	if err := budgetPlacementPodHealthy(related.pod); err != nil {
		return err
	}
	if err := budgetPlacementPodTemplateMatches(deployment, related); err != nil {
		return err
	}
	if err := s.verifyPodPlacementPhase(target, related.pod, phase); err != nil {
		return err
	}
	return s.verifyPodNode(ctx, related.pod, phase == "baseline" && target.writeSelector)
}

func budgetPlacementPodHealthy(pod *corev1.Pod) error {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning || pod.Spec.NodeName == "" || !budgetPlacementPodReady(pod) {
		return fmt.Errorf("Pod %s/%s uid=%s is not a healthy bound non-terminating Pod: phase=%s node=%s conditions=%v containers=%v", pod.Namespace, pod.Name, pod.UID, pod.Status.Phase, pod.Spec.NodeName, pod.Status.Conditions, pod.Status.ContainerStatuses)
	}
	return nil
}

func budgetPlacementPodTemplateMatches(deployment *appsv1.Deployment, related budgetPlacementPod) error {
	pod, rs := related.pod, related.replicaSet
	if rs.DeletionTimestamp != nil || !maps.Equal(rs.Spec.Template.Spec.NodeSelector, deployment.Spec.Template.Spec.NodeSelector) {
		return fmt.Errorf("Pod %s/%s uid=%s still uses an old ReplicaSet selector %v, current template=%v", pod.Namespace, pod.Name, pod.UID, rs.Spec.Template.Spec.NodeSelector, deployment.Spec.Template.Spec.NodeSelector)
	}
	for key, value := range deployment.Spec.Template.Spec.NodeSelector {
		if actual, exists := pod.Spec.NodeSelector[key]; !exists || actual != value {
			return fmt.Errorf("Pod %s/%s uid=%s lacks template selector %s=%s", pod.Namespace, pod.Name, pod.UID, key, value)
		}
	}
	return nil
}

func (s *replacementBudgetPlacement) verifyPodPlacementPhase(target *budgetPlacementTarget, pod *corev1.Pod, phase string) error {
	switch phase {
	case "baseline":
		return nil
	case "isolated":
		if !replacementBudgetPodExcluded(s.pool, pod) || target.writeSelector && target.beforeActivation.Has(pod.UID) {
			return fmt.Errorf("Pod %s/%s uid=%s is old or not hard-isolated: selector=%v affinity=%v tolerations=%v", pod.Namespace, pod.Name, pod.UID, pod.Spec.NodeSelector, pod.Spec.Affinity, pod.Spec.Tolerations)
		}
	case "restored":
		if target.beforeRelease.Has(pod.UID) || !maps.Equal(pod.Spec.NodeSelector, target.baselinePodSelector) {
			return fmt.Errorf("Pod %s/%s uid=%s has not restored effective placement: selector=%v baseline=%v", pod.Namespace, pod.Name, pod.UID, pod.Spec.NodeSelector, target.baselinePodSelector)
		}
	default:
		return fmt.Errorf("unknown placement verification phase %q", phase)
	}
	return nil
}

func (s *replacementBudgetPlacement) verifyPodNode(ctx context.Context, pod *corev1.Pod, requireSystem bool) error {
	node, err := s.kube.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get actual Node for Pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	if node.DeletionTimestamp != nil || !budgetPlacementNodeReady(node) {
		return fmt.Errorf("actual Node %s for Pod uid=%s is not ready", node.Name, pod.UID)
	}
	// This is an activation feasibility check, not an isolation proof from a
	// current binding: the new selector must retain the currently healthy system
	// placements. Moving an owner from other pools requires a different contract.
	if requireSystem && node.Labels[v1beta1.AKSLabelMode] != v1beta1.ModeSystem {
		return fmt.Errorf("cannot safely pin Pod uid=%s: its healthy placement on Node %s is not system mode", pod.UID, node.Name)
	}
	for key, value := range pod.Spec.NodeSelector {
		if actual, exists := node.Labels[key]; !exists || actual != value {
			return fmt.Errorf("actual Node %s does not satisfy Pod uid=%s selector %s=%s", node.Name, pod.UID, key, value)
		}
	}
	return nil
}

func budgetPlacementPodReady(pod *corev1.Pod) bool {
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			ready = condition.Status == corev1.ConditionTrue
		}
	}
	if !ready || len(pod.Spec.Containers) == 0 || len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) {
		return false
	}
	for _, container := range pod.Spec.Containers {
		if !budgetPlacementContainerReady(container.Name, pod.Status.ContainerStatuses) {
			return false
		}
	}
	return true
}

func budgetPlacementContainerReady(name string, statuses []corev1.ContainerStatus) bool {
	for _, status := range statuses {
		if status.Name == name && status.Ready && status.State.Running != nil {
			return true
		}
	}
	return false
}

func budgetPlacementNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (s *replacementBudgetPlacement) waitForTargets(ctx context.Context, targets []*budgetPlacementTarget, restoring bool) error {
	var lastObservation error
	phase := "isolated"
	if restoring {
		phase = "restored"
	}
	err := wait.PollUntilContextTimeout(ctx, s.pollInterval, s.timeout, true, func(ctx context.Context) (bool, error) {
		var observations []error
		for _, target := range targets {
			deployment, err := s.checkTargetIntent(ctx, target, restoring)
			if err != nil {
				return false, err // Identity/ownership changes cannot be fixed by waiting or forcing.
			}
			if _, err := s.rolloutReady(ctx, deployment, target, phase); err != nil {
				target.lastObservation = err.Error()
				observations = append(observations, err)
			} else {
				target.lastObservation = phase + " and healthy"
			}
		}
		if len(observations) != 0 {
			lastObservation = errors.Join(observations...)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for %s placement: %w; last observation: %v", phase, err, lastObservation)
	}
	return nil
}

func (s *replacementBudgetPlacement) checkTargetIntent(ctx context.Context, target *budgetPlacementTarget, restoring bool) (*appsv1.Deployment, error) {
	deployment, err := s.getDeployment(ctx, target.key, target.uid)
	if err != nil {
		return nil, err
	}
	if restoring {
		err = s.validateRestored(deployment)
	} else if target.writeSelector {
		err = s.validateOwned(deployment)
	} else if !replacementBudgetPodExcluded(s.pool, &corev1.Pod{Spec: deployment.Spec.Template.Spec}) {
		err = fmt.Errorf("read-only Deployment %s no longer has an excluding template", target.key)
	}
	return deployment, err
}

func (s *replacementBudgetPlacement) Verify(ctx context.Context, fixtures []*appsv1.Deployment) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if !s.active {
		return fmt.Errorf("placement has not completed activation or was already restored")
	}
	if err := s.waitForTargets(ctx, s.targets, false); err != nil {
		return err
	}
	return s.verifyUnrelated(ctx, fixtures)
}

func (s *replacementBudgetPlacement) verifyUnrelated(ctx context.Context, fixtures []*appsv1.Deployment) error {
	allowed := map[types.UID]types.NamespacedName{}
	for _, fixture := range fixtures {
		if fixture == nil {
			return fmt.Errorf("cannot authorize a nil budget Deployment")
		}
		key := types.NamespacedName{Namespace: fixture.Namespace, Name: fixture.Name}
		if _, err := s.getDeployment(ctx, key, fixture.UID); err != nil {
			return fmt.Errorf("verify budget-owned Deployment identity: %w", err)
		}
		allowed[fixture.UID] = key
	}
	pods, err := s.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("recheck unrelated Pods: %w", err)
	}
	for i := range pods.Items {
		if err := s.verifyUnrelatedPod(ctx, &pods.Items[i], allowed); err != nil {
			return err
		}
	}
	return nil
}

func (s *replacementBudgetPlacement) verifyUnrelatedPod(ctx context.Context, pod *corev1.Pod, allowed map[types.UID]types.NamespacedName) error {
	if podutils.IsOwnedByDaemonSet(pod) || replacementBudgetTaintExcludes(s.pool, pod) {
		return nil
	}
	if _, err := budgetPlacementController(pod, "ReplicaSet"); err != nil {
		if replacementBudgetHardExcludes(s.pool, &pod.Spec) {
			return nil
		}
		return fmt.Errorf("compatible unrelated Pod %s/%s uid=%s: %w", pod.Namespace, pod.Name, pod.UID, err)
	}
	deployment, err := s.resolveDeployment(ctx, pod)
	if err != nil {
		return err
	}
	if key, ok := allowed[deployment.UID]; ok && key == (types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}) {
		return nil // Only the fixture's exact Deployment UIDs, never names or labels.
	}
	if !replacementBudgetPodExcluded(s.pool, pod) || !replacementBudgetPodExcluded(s.pool, &corev1.Pod{Spec: deployment.Spec.Template.Spec}) {
		return fmt.Errorf("unrelated Pod %s/%s uid=%s or owner Deployment %s uid=%s can use the budget pool: pod selector=%v affinity=%v template selector=%v affinity=%v", pod.Namespace, pod.Name, pod.UID, deployment.Name, deployment.UID, pod.Spec.NodeSelector, pod.Spec.Affinity, deployment.Spec.Template.Spec.NodeSelector, deployment.Spec.Template.Spec.Affinity)
	}
	return nil
}

func (s *replacementBudgetPlacement) Restore(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.active = false
	// Never use an activation context captured by the cleanup closure. The
	// registered Ginkgo cleanup supplies its own fresh bounded SpecContext.
	var failures []error
	var restored []*budgetPlacementTarget
	for i := len(s.targets) - 1; i >= 0; i-- {
		target := s.targets[i]
		if !target.attempted {
			continue
		}
		readyToWait, err := s.restoreTarget(ctx, target)
		if err != nil {
			failures = append(failures, fmt.Errorf("restore Deployment %s uid=%s: %w", target.key, target.uid, err))
		}
		if readyToWait {
			restored = append(restored, target)
		}
	}
	if len(restored) > 0 {
		if err := s.waitForTargets(ctx, restored, true); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (target *budgetPlacementTarget) resolveSubmittedApplies(current *appsv1.Deployment) error {
	// Callers supply getDeployment's validated, uncached current object. RVs are
	// opaque: for the same UID, a different current RV invalidates the submitted
	// old-RV CAS. Absence, another GET, or an acknowledged but unobserved response
	// cannot invalidate a request whose preconditions are still current.
	if len(target.submittedApplies) == 0 {
		return fmt.Errorf("cannot resolve attempted selector mutation for %s without submitted preconditions", target.key)
	}
	var unresolved []error
	for i := range target.submittedApplies {
		submitted := &target.submittedApplies[i]
		if submitted.uid == "" || submitted.uid != current.UID || submitted.resourceVersion == "" {
			return fmt.Errorf("cannot fence selector apply for %s: submitted uid=%s rv=%s, current uid=%s rv=%s", target.key, submitted.uid, submitted.resourceVersion, current.UID, current.ResourceVersion)
		}
		switch submitted.outcome {
		case budgetPlacementApplyRejected:
			continue
		case budgetPlacementApplyUnknown, budgetPlacementApplyAcknowledged:
			if current.ResourceVersion != submitted.resourceVersion {
				// Record the fence separately: a GET does not retroactively tell
				// us whether an unknown request committed or was rejected.
				submitted.fencedAtResourceVersion = current.ResourceVersion
				continue
			}
			unresolved = append(unresolved, fmt.Errorf("unresolved selector apply for %s uid=%s submitted rv=%s install=%t outcome=%s: unchanged current rv=%s is not a completion fence", target.key, submitted.uid, submitted.resourceVersion, submitted.install, submitted.outcome, current.ResourceVersion))
		default:
			return fmt.Errorf("unrecognized selector apply outcome %q for %s uid=%s rv=%s", submitted.outcome, target.key, submitted.uid, submitted.resourceVersion)
		}
	}
	return errors.Join(unresolved...)
}

func (s *replacementBudgetPlacement) restoreTarget(ctx context.Context, target *budgetPlacementTarget) (bool, error) {
	deployment, err := s.getDeployment(ctx, target.key, target.uid)
	if err != nil {
		return false, err // Missing/replaced objects are discrepancies, not successful cleanup.
	}
	if target.released {
		return true, s.validateRestored(deployment)
	}
	if err := s.validateRestored(deployment); err == nil {
		releaseMayHavePersisted := false
		for _, submitted := range target.submittedApplies {
			if !submitted.install && submitted.outcome != budgetPlacementApplyRejected {
				releaseMayHavePersisted = true
			}
		}
		if target.confirmed && !releaseMayHavePersisted {
			return false, fmt.Errorf("confirmed selector intent disappeared before cleanup; refusing to mask lost ownership")
		}
		// A lost omission response can explain absence after cleanup began. It
		// still needs every submitted precondition fenced, not just this GET.
		if err := target.resolveSubmittedApplies(deployment); err != nil {
			// Leave the target recoverable: a later Restore can observe and
			// omit our selector if an unknown apply subsequently persists.
			return false, err
		}
		// Absence is final only after every submitted request was rejected or
		// fenced. Never manufacture that proof with a no-op or unrelated write.
		target.released = true
		return true, nil
	}
	if err := s.validateOwned(deployment); err != nil {
		return false, err // Preserve any co-owned/foreign selector and report the discrepancy.
	}
	target.beforeRelease = sets.New[types.UID]()
	pods, observationErr := s.ownedPods(ctx, deployment)
	for _, related := range pods {
		// After interrupted activation, healthy original-template Pods may still
		// exist. They already have the restored placement and need not be deleted.
		// Only Pods from the temporarily selected template must disappear.
		if len(related.replicaSet.Spec.Template.Spec.NodeSelector) != 0 {
			target.beforeRelease.Insert(related.pod.UID)
		}
	}
	// Even if Pod inventory failed, relinquish our safely verified intent. Keep
	// the inventory error visible and still validate the resulting rollout.
	if err := s.mutateSelector(ctx, target, false); err != nil {
		return false, errors.Join(observationErr, err)
	}
	return true, observationErr
}

func (s *replacementBudgetPlacement) Diagnostics() map[string]interface{} {
	targets := make([]map[string]interface{}, 0, len(s.targets))
	for _, target := range s.targets {
		submitted := make([]map[string]interface{}, 0, len(target.submittedApplies))
		for _, apply := range target.submittedApplies {
			submitted = append(submitted, map[string]interface{}{
				"uid": apply.uid, "resourceVersion": apply.resourceVersion, "install": apply.install,
				"outcome": string(apply.outcome), "fencedAtResourceVersion": apply.fencedAtResourceVersion,
			})
		}
		targets = append(targets, map[string]interface{}{
			"deployment": target.key.String(), "uid": target.uid, "writeSelector": target.writeSelector,
			"attempted": target.attempted, "confirmed": target.confirmed, "released": target.released,
			"submittedApplies":    submitted,
			"baselinePodSelector": target.baselinePodSelector, "lastObservation": target.lastObservation,
		})
	}
	return map[string]interface{}{"fieldManager": s.manager, "targets": targets}
}
