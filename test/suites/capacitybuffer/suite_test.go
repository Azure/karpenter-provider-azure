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

package capacitybuffer_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	autoscalingv1beta1 "sigs.k8s.io/karpenter/pkg/apis/autoscaling/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool
var originalSettings []corev1.EnvVar
var originalCoreRoleRules []rbacv1.PolicyRule
var capacityBufferConfigured bool

func TestCapacityBuffer(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
		if env.InClusterController {
			enableCapacityBuffer()
		}
	})
	AfterSuite(func() {
		if capacityBufferConfigured {
			restoreCapacityBufferConfiguration()
		}
		env.Stop()
	})
	RunSpecs(t, "CapacityBuffer")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	// Stop buffers from creating replacement NodeClaims while the general
	// concurrent object cleanup is sweeping NodeClaims and NodePools.
	env.CleanupObjects(&autoscalingv1beta1.CapacityBuffer{}, &corev1.PodTemplate{})
	env.Cleanup()
})
var _ = AfterEach(func() { env.AfterEach() })

func enableCapacityBuffer() {
	GinkgoHelper()
	originalSettings = env.ExpectSettings()

	coreRole, err := env.KubeClient.RbacV1().ClusterRoles().Get(env.Context, "karpenter-core", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	originalCoreRoleRules = coreRole.DeepCopy().Rules
	coreRole.Rules = append(coreRole.Rules, capacityBufferPolicyRules()...)
	_, err = env.KubeClient.RbacV1().ClusterRoles().Update(env.Context, coreRole, metav1.UpdateOptions{})
	Expect(err).ToNot(HaveOccurred())

	capacityBufferConfigured = true
	env.ExpectSettingsOverridden(withCapacityBufferEnabled(originalSettings))
}

func restoreCapacityBufferConfiguration() {
	GinkgoHelper()
	// Disable the controllers before removing the permissions they use.
	env.ExpectSettingsReplaced(originalSettings...)

	coreRole, err := env.KubeClient.RbacV1().ClusterRoles().Get(env.Context, "karpenter-core", metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	coreRole.Rules = originalCoreRoleRules
	_, err = env.KubeClient.RbacV1().ClusterRoles().Update(env.Context, coreRole, metav1.UpdateOptions{})
	Expect(err).ToNot(HaveOccurred())
}

func withCapacityBufferEnabled(settings []corev1.EnvVar) corev1.EnvVar {
	GinkgoHelper()
	featureGates := ""
	for _, setting := range settings {
		if setting.Name == "FEATURE_GATES" {
			featureGates = setting.Value
			break
		}
	}

	parts := strings.Split(featureGates, ",")
	found := false
	for i, part := range parts {
		key, _, ok := strings.Cut(part, "=")
		if ok && key == "CapacityBuffer" {
			parts[i] = "CapacityBuffer=true"
			found = true
		}
	}
	if !found {
		parts = append(parts, "CapacityBuffer=true")
	}
	return corev1.EnvVar{Name: "FEATURE_GATES", Value: strings.Trim(strings.Join(parts, ","), ",")}
}

func capacityBufferPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{"autoscaling.x-k8s.io"},
			Resources: []string{"capacitybuffers"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"autoscaling.x-k8s.io"},
			Resources: []string{"capacitybuffers/status"},
			Verbs:     []string{"update", "patch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"podtemplates"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
}
