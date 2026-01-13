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

package fake

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

// Note that we want this to represent the real contract, rather than the fake
func TestNodeBootstrappingAPI_Get_Success(t *testing.T) {
	g := NewWithT(t)
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	nb, err := api.Get(context.TODO(), params)
	g.Expect(err).ToNot(HaveOccurred())

	// Suggestion: verify legitimacy of CSE + CustomData
	// However, given that Karpenter does not contain that knowledge, the validation logic should be imported from CRP, if there is a well-contained one
	// Even the omit of bootstrap token, where not having them in the first place is a valid case, and Karpenter's contract is to only replace only if they (the templated fields) are present

	_, err = base64.StdEncoding.DecodeString(nb.CustomDataEncodedDehydratable)
	g.Expect(err).ToNot(HaveOccurred()) // CustomData should be encoded
	_, err = base64.StdEncoding.DecodeString(nb.CSEDehydratable)
	g.Expect(err).To(HaveOccurred()) // CSE should not be encoded
}

func TestNodeBootstrappingAPI_Get_MissingProvisionProfile(t *testing.T) {
	g := NewWithT(t)
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      nil,
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	_, err := api.Get(context.TODO(), params)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("MissingRequiredProperty"))
	g.Expect(err.Error()).To(ContainSubstring("ProvisionProfile cannot be empty"))
}

func TestNodeBootstrappingAPI_Get_MissingProvisionHelperValues(t *testing.T) {
	g := NewWithT(t)
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: nil,
	}

	_, err := api.Get(context.TODO(), params)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("MissingRequiredProperty"))
	g.Expect(err.Error()).To(ContainSubstring("ProvisionHelperValues cannot be empty"))
}

// Helper functions
func createValidProvisionProfile() *models.ProvisionProfile {
	return &models.ProvisionProfile{
		Name:                lo.ToPtr("test-node"),
		VMSize:              lo.ToPtr("Standard_D2s_v3"),
		OsType:              lo.ToPtr(models.OSTypeLinux),
		OsSku:               lo.ToPtr(models.OSSKUAzureLinux),
		StorageProfile:      lo.ToPtr(consts.StorageProfileManagedDisks),
		Distro:              lo.ToPtr("AzureLinux"),
		OrchestratorVersion: lo.ToPtr("1.31.0"),
		VnetCidrs:           []string{"10.0.0.0/8"},
		VnetSubnetID:        lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"),
		Mode:                lo.ToPtr(models.AgentPoolModeSystem),
		Architecture:        lo.ToPtr("amd64"),
		MaxPods:             lo.ToPtr(int32(110)),
	}
}

func createValidProvisionHelperValues() *models.ProvisionHelperValues {
	return &models.ProvisionHelperValues{
		SkuCPU:    lo.ToPtr(float64(2)),
		SkuMemory: lo.ToPtr(float64(8192)),
	}
}

func TestNodeBootstrappingAPI_RecordsRequests(t *testing.T) {
	g := NewWithT(t)
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	_, err := api.Get(context.TODO(), params)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify that the request was recorded
	g.Expect(api.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
	recordedInput := api.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
	g.Expect(recordedInput.Params).To(Equal(params))

	// Verify call counts
	g.Expect(api.NodeBootstrappingGetBehavior.Calls()).To(Equal(1))
	g.Expect(api.NodeBootstrappingGetBehavior.SuccessfulCalls()).To(Equal(1))
	g.Expect(api.NodeBootstrappingGetBehavior.FailedCalls()).To(Equal(0))
}

func TestNodeBootstrappingAPI_RecordsMultipleRequests(t *testing.T) {
	g := NewWithT(t)
	api := &NodeBootstrappingAPI{}

	params1 := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	params2 := &models.ProvisionValues{
		ProvisionProfile: &models.ProvisionProfile{
			Name:                lo.ToPtr("test-node-2"),
			VMSize:              lo.ToPtr("Standard_D4s_v3"),
			OsType:              lo.ToPtr(models.OSTypeLinux),
			OsSku:               lo.ToPtr(models.OSSKUAzureLinux),
			StorageProfile:      lo.ToPtr(consts.StorageProfileManagedDisks),
			Distro:              lo.ToPtr("AzureLinux"),
			OrchestratorVersion: lo.ToPtr("1.31.0"),
			VnetCidrs:           []string{"10.0.0.0/8"},
			VnetSubnetID:        lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"),
			Mode:                lo.ToPtr(models.AgentPoolModeSystem),
			Architecture:        lo.ToPtr("amd64"),
			MaxPods:             lo.ToPtr(int32(110)),
		},
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	_, err := api.Get(context.TODO(), params1)
	g.Expect(err).ToNot(HaveOccurred())

	_, err = api.Get(context.TODO(), params2)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify that both requests were recorded
	g.Expect(api.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(2))
	g.Expect(api.NodeBootstrappingGetBehavior.Calls()).To(Equal(2))
	g.Expect(api.NodeBootstrappingGetBehavior.SuccessfulCalls()).To(Equal(2))

	// Verify requests in LIFO order (stack behavior)
	recordedInput2 := api.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
	g.Expect(recordedInput2.Params).To(Equal(params2))

	recordedInput1 := api.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
	g.Expect(recordedInput1.Params).To(Equal(params1))
}

func TestNodeBootstrappingAPI_Reset(t *testing.T) {
	g := NewWithT(t)
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	_, err := api.Get(context.TODO(), params)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(api.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))

	// Reset should clear recorded requests
	api.Reset()
	g.Expect(api.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(0))
}
