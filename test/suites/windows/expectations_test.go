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

package windows_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

func expectWindowsProvisioningRelationships(settings windowsImageSettings, nodePool *karpv1.NodePool, pods []*corev1.Pod, expectedCount int) {
	GinkgoHelper()

	nodePoolSelector := labels.SelectorFromSet(map[string]string{karpv1.NodePoolLabelKey: nodePool.Name})
	nodeClaims := env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", expectedCount, nodePoolSelector)
	nodes := env.EventuallyExpectNodeCountWithSelector("==", expectedCount, nodePoolSelector)
	machines := env.EventuallyExpectCreatedMachineCount("==", expectedCount)

	registeredNodeClaims := map[string]*karpv1.NodeClaim{}
	for _, nodeClaim := range nodeClaims {
		if nodeClaim.StatusConditions().IsTrue(karpv1.ConditionTypeRegistered) {
			registeredNodeClaims[nodeClaim.Name] = nodeClaim
		}
	}
	Expect(registeredNodeClaims).To(HaveLen(expectedCount))

	nodesByProviderID := map[string]*corev1.Node{}
	nodesByName := map[string]*corev1.Node{}
	for _, node := range nodes {
		nodesByProviderID[node.Spec.ProviderID] = node
		nodesByName[node.Name] = node
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Windows)))
		Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelOSSKU, settings.expectedSKU))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
		if settings.expectedFIPS {
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelFIPSEnabled, "true"))
		} else {
			Expect(node.Labels).ToNot(HaveKey(v1beta1.AKSLabelFIPSEnabled))
		}
	}

	machinesByNodeClaim := map[string]*containerservice.Machine{}
	for _, machine := range machines {
		Expect(machine.Properties).ToNot(BeNil())
		nodeClaimTag, ok := machine.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey]
		Expect(ok).To(BeTrue())
		Expect(nodeClaimTag).ToNot(BeNil())
		nodeClaimName := *nodeClaimTag
		Expect(nodeClaimName).ToNot(BeEmpty())
		nodePoolTag, ok := machine.Properties.Tags[launchtemplate.NodePoolTagKey]
		Expect(ok).To(BeTrue())
		Expect(nodePoolTag).ToNot(BeNil())
		Expect(*nodePoolTag).To(Equal(nodePool.Name))
		machinesByNodeClaim[nodeClaimName] = machine
	}
	Expect(machinesByNodeClaim).To(HaveLen(expectedCount))

	for nodeClaimName, nodeClaim := range registeredNodeClaims {
		Expect(nodeClaim.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))

		machine, ok := machinesByNodeClaim[nodeClaimName]
		Expect(ok).To(BeTrue(), "expected a Machine tagged for NodeClaim %s", nodeClaimName)
		Expect(machine.ID).ToNot(BeNil())
		Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSMachineResourceID, *machine.ID))

		Expect(machine.Properties.ResourceID).ToNot(BeNil())
		expectedProviderID := utils.VMResourceIDToProviderID(env.Context, *machine.Properties.ResourceID)
		Expect(nodeClaim.Status.ProviderID).To(Equal(expectedProviderID))
		node, ok := nodesByProviderID[expectedProviderID]
		Expect(ok).To(BeTrue(), "expected a Node with provider ID %s", expectedProviderID)

		Expect(machine.Properties.Kubernetes).ToNot(BeNil())
		Expect(machine.Properties.Kubernetes.NodeName).ToNot(BeNil())
		Expect(*machine.Properties.Kubernetes.NodeName).To(Equal(node.Name))
		Expect(node.Spec.ProviderID).To(Equal(nodeClaim.Status.ProviderID))

		Expect(machine.Properties.NodeImageVersion).ToNot(BeNil())
		Expect(*machine.Properties.NodeImageVersion).To(MatchRegexp(settings.expectedImagePattern))
		Expect(nodeClaim.Status.ImageID).To(Equal(*machine.Properties.NodeImageVersion))

		Expect(machine.Properties.OperatingSystem).ToNot(BeNil())
		Expect(machine.Properties.OperatingSystem.OSType).ToNot(BeNil())
		Expect(*machine.Properties.OperatingSystem.OSType).To(Equal(containerservice.OSTypeWindows))
		Expect(machine.Properties.OperatingSystem.OSSKU).ToNot(BeNil())
		Expect(string(*machine.Properties.OperatingSystem.OSSKU)).To(Equal(settings.expectedSKU))
		Expect(machine.Properties.OperatingSystem.EnableFIPS).ToNot(BeNil())
		Expect(*machine.Properties.OperatingSystem.EnableFIPS).To(Equal(settings.expectedFIPS))
	}

	for _, pod := range pods {
		Expect(pod.Spec.NodeName).ToNot(BeEmpty())
		_, ok := nodesByName[pod.Spec.NodeName]
		Expect(ok).To(BeTrue(), "expected pod %s/%s to run on a Node owned by %s", pod.Namespace, pod.Name, nodePool.Name)
	}
}
