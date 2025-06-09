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

package networksecuritygroup_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

var ctx context.Context
var stop context.CancelFunc

var resourceGroup string
var fakeNetworkSecurityGroupsAPI *fake.NetworkSecurityGroupAPI
var networkSecurityGroupProvider *networksecuritygroup.Provider

func TestNetworkSecurityGroupProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Providers/NetworkSecurityGroup")
}

var _ = BeforeSuite(func() {
	ctx, stop = context.WithCancel(ctx)

	fakeNetworkSecurityGroupsAPI = &fake.NetworkSecurityGroupAPI{}
	resourceGroup = "test-rg"
})

var _ = AfterSuite(func() {
	stop()
})

var _ = BeforeEach(func() {
	networkSecurityGroupProvider = networksecuritygroup.NewProvider(fakeNetworkSecurityGroupsAPI, resourceGroup)
	fakeNetworkSecurityGroupsAPI.Reset()
})

var _ = Describe("NetworkSecurityGroup Provider", func() {
	It("should return only well-known network security groups", func() {
		standardNSG := test.MakeNetworkSecurityGroup(resourceGroup, "aks-agentpool-12345678-nsg")
		otherNSG := test.MakeNetworkSecurityGroup(resourceGroup, "some-nsg")

		fakeNetworkSecurityGroupsAPI.NSGs.Store(standardNSG.ID, standardNSG)
		fakeNetworkSecurityGroupsAPI.NSGs.Store(otherNSG.ID, otherNSG)

		nsg, err := networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(nsg).ToNot(BeNil())
		Expect(*nsg).To(Equal(standardNSG))
	})

	It("should error if it cannot find the managed network security group", func() {
		otherNSG := test.MakeNetworkSecurityGroup(resourceGroup, "some-nsg")

		fakeNetworkSecurityGroupsAPI.NSGs.Store(otherNSG.ID, otherNSG)

		_, err := networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
		Expect(err).To(MatchError("couldn't find managed NSG"))
	})

	It("should error if it finds multiple managed network security groups", func() {
		standardNSG1 := test.MakeNetworkSecurityGroup(resourceGroup, "aks-agentpool-12345678-nsg")
		standardNSG2 := test.MakeNetworkSecurityGroup(resourceGroup, "aks-agentpool-23456789-nsg")

		fakeNetworkSecurityGroupsAPI.NSGs.Store(standardNSG1.ID, standardNSG1)
		fakeNetworkSecurityGroupsAPI.NSGs.Store(standardNSG2.ID, standardNSG2)

		_, err := networkSecurityGroupProvider.ManagedNetworkSecurityGroup(ctx)
		Expect(err).To(MatchError("found multiple NSGs: aks-agentpool-12345678-nsg,aks-agentpool-23456789-nsg"))
	})
})
