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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
	nodeutils "sigs.k8s.io/karpenter/pkg/utils/node"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

func expectResolvedWindowsImages(nodeClass *v1beta1.AKSNodeClass) []v1beta1.NodeImage {
	GinkgoHelper()

	var images []v1beta1.NodeImage
	Eventually(func(g Gomega) {
		resolvedNodeClass := &v1beta1.AKSNodeClass{}
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), resolvedNodeClass)).To(Succeed())
		var err error
		images, err = resolvedNodeClass.GetImages()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(images).ToNot(BeEmpty())
	}).Should(Succeed())
	return append([]v1beta1.NodeImage(nil), images...)
}

func expectWindowsProvisioningRelationships(settings windowsImageSettings, images []v1beta1.NodeImage, nodePool *karpv1.NodePool, pods []*corev1.Pod, expectedWindowsCount, expectedTotalMachineCount int) {
	GinkgoHelper()

	nodePoolSelector := labels.SelectorFromSet(map[string]string{karpv1.NodePoolLabelKey: nodePool.Name})
	nodeClaims := env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", expectedWindowsCount, nodePoolSelector)
	nodes := env.EventuallyExpectNodeCountWithSelector("==", expectedWindowsCount, nodePoolSelector)
	machines := env.EventuallyExpectCreatedMachineCount("==", expectedTotalMachineCount)

	registeredNodeClaims := indexRegisteredNodeClaims(nodeClaims, expectedWindowsCount)
	nodesByProviderID, nodesByName := indexWindowsNodes(settings, nodePool, nodes)
	machinesByNodeClaim := indexWindowsMachines(nodePool, registeredNodeClaims, machines, expectedWindowsCount)

	for nodeClaimName, nodeClaim := range registeredNodeClaims {
		expectWindowsMachineRelationship(settings, images, nodePool, nodeClaimName, nodeClaim, machinesByNodeClaim[nodeClaimName], nodesByProviderID)
	}
	expectPodsOnNodePool(pods, nodesByName, nodePool)
}

func indexRegisteredNodeClaims(nodeClaims []*karpv1.NodeClaim, expectedCount int) map[string]*karpv1.NodeClaim {
	GinkgoHelper()

	registeredNodeClaims := map[string]*karpv1.NodeClaim{}
	for _, nodeClaim := range nodeClaims {
		if nodeClaim.StatusConditions().IsTrue(karpv1.ConditionTypeRegistered) {
			registeredNodeClaims[nodeClaim.Name] = nodeClaim
		}
	}
	Expect(registeredNodeClaims).To(HaveLen(expectedCount))
	return registeredNodeClaims
}

func indexWindowsNodes(settings windowsImageSettings, nodePool *karpv1.NodePool, nodes []*corev1.Node) (map[string]*corev1.Node, map[string]*corev1.Node) {
	GinkgoHelper()

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
	return nodesByProviderID, nodesByName
}

func indexWindowsMachines(nodePool *karpv1.NodePool, nodeClaims map[string]*karpv1.NodeClaim, machines []*containerservice.Machine, expectedCount int) map[string]*containerservice.Machine {
	GinkgoHelper()

	machinesByNodeClaim := map[string]*containerservice.Machine{}
	for _, machine := range machines {
		if machine.Properties == nil {
			continue
		}
		nodeClaimTag := machine.Properties.Tags[launchtemplate.KarpenterAKSMachineNodeClaimTagKey]
		if nodeClaimTag == nil {
			continue
		}
		nodeClaimName := *nodeClaimTag
		if _, ok := nodeClaims[nodeClaimName]; !ok {
			continue
		}
		nodePoolTag, ok := machine.Properties.Tags[launchtemplate.NodePoolTagKey]
		Expect(ok).To(BeTrue())
		Expect(nodePoolTag).ToNot(BeNil())
		Expect(*nodePoolTag).To(Equal(nodePool.Name))
		machinesByNodeClaim[nodeClaimName] = machine
	}
	Expect(machinesByNodeClaim).To(HaveLen(expectedCount))
	return machinesByNodeClaim
}

func expectWindowsMachineRelationship(settings windowsImageSettings, images []v1beta1.NodeImage, nodePool *karpv1.NodePool, nodeClaimName string, nodeClaim *karpv1.NodeClaim, machine *containerservice.Machine, nodesByProviderID map[string]*corev1.Node) {
	GinkgoHelper()

	Expect(nodeClaim.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
	Expect(machine).ToNot(BeNil(), "expected a Machine tagged for NodeClaim %s", nodeClaimName)
	Expect(machine.ID).ToNot(BeNil())
	Expect(nodeClaim.Annotations).To(HaveKeyWithValue(v1beta1.AnnotationAKSMachineResourceID, *machine.ID))

	Expect(machine.Properties.ResourceID).ToNot(BeNil())
	expectedProviderID := utils.VMResourceIDToProviderID(env.Context, *machine.Properties.ResourceID)
	Expect(nodeClaim.Status.ProviderID).To(Equal(expectedProviderID))
	node, ok := nodesByProviderID[expectedProviderID]
	Expect(ok).To(BeTrue(), "expected a Node with provider ID %s", expectedProviderID)
	Expect(nodeutils.GetCondition(node, corev1.NodeReady).Status).To(Equal(corev1.ConditionTrue))

	Expect(machine.Properties.Kubernetes).ToNot(BeNil())
	Expect(machine.Properties.Kubernetes.NodeName).ToNot(BeNil())
	Expect(*machine.Properties.Kubernetes.NodeName).To(Equal(node.Name))
	Expect(node.Spec.ProviderID).To(Equal(nodeClaim.Status.ProviderID))

	expectedNodeImageVersion := expectedWindowsNodeImageVersion(settings, images, node)
	Expect(machine.Properties.NodeImageVersion).ToNot(BeNil())
	Expect(*machine.Properties.NodeImageVersion).To(Equal(expectedNodeImageVersion))
	Expect(nodeClaim.Status.ImageID).To(Equal(expectedNodeImageVersion))
	Expect(node.Labels).To(HaveKeyWithValue("kubernetes.azure.com/node-image-version", expectedNodeImageVersion))

	Expect(machine.Properties.OperatingSystem).ToNot(BeNil())
	Expect(machine.Properties.OperatingSystem.OSType).ToNot(BeNil())
	Expect(*machine.Properties.OperatingSystem.OSType).To(Equal(containerservice.OSTypeWindows))
	Expect(machine.Properties.OperatingSystem.OSSKU).ToNot(BeNil())
	Expect(string(*machine.Properties.OperatingSystem.OSSKU)).To(Equal(settings.expectedSKU))
	Expect(machine.Properties.OperatingSystem.EnableFIPS).ToNot(BeNil())
	Expect(*machine.Properties.OperatingSystem.EnableFIPS).To(Equal(settings.expectedFIPS))
}

func expectedWindowsNodeImageVersion(settings windowsImageSettings, images []v1beta1.NodeImage, node *corev1.Node) string {
	GinkgoHelper()

	nodeRequirements := scheduling.NewLabelRequirements(node.Labels)
	for _, image := range images {
		if nodeRequirements.Compatible(scheduling.NewNodeSelectorRequirements(image.Requirements...), v1beta1.AllowUndefinedWellKnownAndRestrictedLabels) == nil {
			expectedNodeImageVersion, err := utils.GetAKSMachineNodeImageVersionFromImageID(image.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(expectedNodeImageVersion).To(MatchRegexp(settings.expectedImagePattern))
			return expectedNodeImageVersion
		}
	}
	Fail(fmt.Sprintf("expected a NodeClass status image compatible with node %s", node.Name))
	return ""
}

func expectPodsOnNodePool(pods []*corev1.Pod, nodesByName map[string]*corev1.Node, nodePool *karpv1.NodePool) {
	GinkgoHelper()

	for _, pod := range pods {
		Expect(pod.Spec.NodeName).ToNot(BeEmpty())
		_, ok := nodesByName[pod.Spec.NodeName]
		Expect(ok).To(BeTrue(), "expected pod %s/%s to run on a Node owned by %s", pod.Namespace, pod.Name, nodePool.Name)
	}
}

func expectLinuxProvisioningRelationships(nodePool *karpv1.NodePool, pods []*corev1.Pod, expectedCount int) {
	GinkgoHelper()

	nodePoolSelector := labels.SelectorFromSet(map[string]string{karpv1.NodePoolLabelKey: nodePool.Name})
	nodeClaims := env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", expectedCount, nodePoolSelector)
	nodes := env.EventuallyExpectNodeCountWithSelector("==", expectedCount, nodePoolSelector)

	Expect(nodeClaims).To(HaveLen(expectedCount))
	nodesByName := map[string]*corev1.Node{}
	for _, node := range nodes {
		nodesByName[node.Name] = node
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelOSStable, string(corev1.Linux)))
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
	}
	for _, nodeClaim := range nodeClaims {
		Expect(nodeClaim.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
		Expect(nodeClaim.StatusConditions().IsTrue(karpv1.ConditionTypeRegistered)).To(BeTrue())
	}
	expectPodsOnNodePool(pods, nodesByName, nodePool)
}
