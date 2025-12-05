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

package expectations

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/skewer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/metrics"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

func ExpectUnavailable(env *test.Environment, sku *skewer.SKU, zone string, capacityType string) {
	GinkgoHelper()
	Expect(env.UnavailableOfferingsCache.IsUnavailable(sku, zone, capacityType)).To(BeTrue())
}

func ExpectKubeletFlags(_ *test.Environment, customData string, expectedFlags map[string]string) {
	GinkgoHelper()
	kubeletFlags := customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
	for flag, value := range expectedFlags {
		Expect(kubeletFlags).To(ContainSubstring(fmt.Sprintf("--%s=%s", flag, value)))
	}
}

func ExpectDecodedCustomData(env *test.Environment) string {
	GinkgoHelper()
	Expect(env.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

	vm := env.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	return decodedString
}

func ExpectCSEProvisioned(env *test.Environment) armcompute.VirtualMachineExtension {
	GinkgoHelper()
	var cse armcompute.VirtualMachineExtension

	// CSE provisioning is asynchronous, starting after VM creation LRO completes
	Eventually(func() bool {
		GinkgoHelper()
		cseRaw, ok := env.VirtualMachineExtensionsAPI.Extensions.Load("cse-agent-karpenter")
		if ok {
			cse = cseRaw.(armcompute.VirtualMachineExtension)
			return true
		}
		return false
	}).Should((BeTrue()), "Expected CSE extension to be created")

	return cse
}

func ExpectCSENotProvisioned(env *test.Environment) {
	GinkgoHelper()

	time.Sleep(1 * time.Second)
	_, ok := env.VirtualMachineExtensionsAPI.Extensions.Load("cse-agent-karpenter")
	Expect(ok).To(BeFalse(), "Expected CSE extension should not be created, but it was found")
}

// ExpectCleanUp handled the cleanup of all Objects we need within testing that core does not
//
// Core's ExpectCleanedUp function does not currently cleanup ConfigMaps:
// https://github.com/kubernetes-sigs/karpenter/blob/db8df23ffb0b689b116d99597316612c98d382ab/pkg/test/expectations/expectations.go#L244
// TODO: surface this within core and remove this function
func ExpectCleanUp(ctx context.Context, c client.Client) {
	GinkgoHelper()
	wg := sync.WaitGroup{}
	namespaces := &corev1.NamespaceList{}
	Expect(c.List(ctx, namespaces)).To(Succeed())
	for _, object := range []client.Object{
		&corev1.ConfigMap{},
	} {
		for _, namespace := range namespaces.Items {
			wg.Add(1)
			go func(object client.Object, namespace string) {
				GinkgoHelper()
				defer wg.Done()
				defer GinkgoRecover()
				Expect(c.DeleteAllOf(ctx, object, client.InNamespace(namespace),
					&client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: lo.ToPtr(int64(0))}})).ToNot(HaveOccurred())
			}(object, namespace.Name)
		}
	}
	wg.Wait()
}

func ExpectInstanceResourcesHaveTags(ctx context.Context, name string, azureEnv *test.Environment, tags map[string]*string) *armcompute.VirtualMachine {
	GinkgoHelper()

	// The VM should be updated
	updatedVM, err := azureEnv.VMInstanceProvider.Get(ctx, name)
	Expect(err).ToNot(HaveOccurred())

	Expect(updatedVM.Tags).To(Equal(tags), "Expected VM tags to match")
	// Expect the identities to remain unchanged
	Expect(updatedVM.Identity).To(BeNil())

	// The NIC should be updated
	updatedNIC, err := azureEnv.NetworkInterfacesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, name, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(updatedNIC.Tags).To(Equal(tags), "Expected NIC tags to match")

	// The extensions should be updated -- Note that we expect only 1 Extension update here because we're simulating scriptless
	// mode which doesn't have a CSE extension.
	Expect(azureEnv.VirtualMachineExtensionsAPI.VirtualMachineExtensionsUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
	for i := 0; i < 1; i++ {
		extUpdate := azureEnv.VirtualMachineExtensionsAPI.VirtualMachineExtensionsUpdateBehavior.CalledWithInput.Pop().VirtualMachineExtensionUpdate
		Expect(extUpdate).ToNot(BeNil())
		Expect(extUpdate.Tags).ToNot(BeNil())
		Expect(extUpdate.Tags).To(Equal(tags), "Expected VM extension tags to match")
	}

	return updatedVM
}

// TODO: Upstream this?
func ExpectLaunched(ctx context.Context, c client.Client, cloudProvider corecloudprovider.CloudProvider, provisioner *provisioning.Provisioner, pods ...*v1.Pod) {
	GinkgoHelper()
	// Persist objects
	for _, pod := range pods {
		ExpectApplied(ctx, c, pod)
	}
	results, err := provisioner.Schedule(ctx)
	Expect(err).ToNot(HaveOccurred())
	for _, m := range results.NewNodeClaims {
		var nodeClaimName string
		nodeClaimName, err = provisioner.Create(ctx, m, provisioning.WithReason(metrics.ProvisionedReason))
		Expect(err).ToNot(HaveOccurred())
		createdNodeClaim := &karpv1.NodeClaim{}
		Expect(c.Get(ctx, types.NamespacedName{Name: nodeClaimName}, createdNodeClaim)).To(Succeed())
		_, err = ExpectNodeClaimDeployedNoNode(ctx, c, cloudProvider, createdNodeClaim)
		Expect(err).ToNot(HaveOccurred())
	}
}
