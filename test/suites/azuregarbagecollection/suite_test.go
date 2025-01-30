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

package azuregarbagecollection

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *karpv1.NodePool

func TestAcr(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Azure Garbage Collection")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("gc", func() {
	It("should garbage collect network interfaces created by karpenter", func() {
		// Allow all families and choose small skus
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: v1.NodeSelectorRequirement{
				Key:      v1alpha2.LabelSKUFamily,
				Operator: v1.NodeSelectorOpNotIn,
				Values:   []string{},
			}})
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: v1.NodeSelectorRequirement{
				Key:      v1alpha2.LabelSKUCPU,
				Operator: v1.NodeSelectorOpLt,
				Values:   []string{"3"},
			}})

		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 5,
			PodOptions: test.PodOptions{
				ResourceRequirements: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU: resource.MustParse("1.1"),
					},
				},
				Image: "mcr.microsoft.com/oss/kubernetes/pause:3.6",
			},
		})

		env.ExpectCreated(nodePool, nodeClass, deployment)
		env.EventuallyExpectHealthyPodCountWithTimeout(time.Minute*15, labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
		By("Eventually removing any excess network interfaces created by karpenter")
		EventuallyExpectOrphanNicsToBeDeleted(env, nodePool)
	})
})

func EventuallyExpectOrphanNicsToBeDeleted(env *azure.Environment, nodePool *karpv1.NodePool) {
	GinkgoHelper()
	resourceGroup := os.Getenv("AZURE_RESOURCE_GROUP")
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		Expect(err).ToNot(HaveOccurred())
	}
	interfacesClient, err := armnetwork.NewInterfacesClient(subscriptionID, cred, nil)
	Expect(err).ToNot(HaveOccurred())
	Eventually(func() bool {
		nics, err := listAllManagedNetworkInterfaces(env.Context, interfacesClient, resourceGroup)
		Expect(err).ToNot(HaveOccurred())
		fmt.Fprintf(GinkgoWriter, "Found %d network interfaces\n", len(nics))
		fmt.Fprintf(GinkgoWriter, "Found %d nodeclaims\n", env.GetNodeclaimCount())
		return len(nics) == env.GetNodeclaimCount()
	}, time.Minute*15, time.Second*15).Should(BeTrue())
}

func listAllManagedNetworkInterfaces(ctx context.Context, interfacesClient *armnetwork.InterfacesClient, resourceGroup string) ([]*armnetwork.Interface, error) {
	poller := interfacesClient.NewListPager(resourceGroup, nil)
	var nics []*armnetwork.Interface
	for poller.More() {
		page, err := poller.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		nics = append(nics, page.Value...)
	}
	return nics, nil
}
