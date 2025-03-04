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

package nodeclaim_test

import (
	. "github.com/onsi/ginkgo/v2"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	azkarptest "github.com/Azure/karpenter-provider-azure/pkg/test"
)

var _ = Describe("gc", func() {
	It("should garbage collect network interfaces created by karpenter", func() {
		env.ExpectCreatedInterface(armnetwork.Interface{
			Name:     lo.ToPtr("orphan-nic"),
			Location: lo.ToPtr(env.Region),
			Tags:     azkarptest.ManagedTags("default"),
			Properties: &armnetwork.InterfacePropertiesFormat{
				IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
					{
						Name: lo.ToPtr("ip-config"),
						Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
							Primary:                   lo.ToPtr(true),
							Subnet:                    env.GetClusterSubnet(),
							PrivateIPAllocationMethod: lo.ToPtr(armnetwork.IPAllocationMethodDynamic),
						},
					},
				},
			},
		})
		env.EventuallyExpectKarpenterNicsToBeDeleted()
	})
})
