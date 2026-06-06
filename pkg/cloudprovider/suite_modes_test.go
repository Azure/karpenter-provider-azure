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

package cloudprovider

import (
	. "github.com/onsi/gomega"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type provisionModeTestCase struct {
	name                  string
	validateNodeClaim     func(*karpv1.NodeClaim)
	resetCreateCalls      func()
	expectCreateCalls     func()
	expectCreatedResource func()
	resetListCalls        func()
	expectListCalls       func()
	resetGetCalls         func()
	expectGetCalls        func()
	resetDeleteCalls      func()
	expectDeleteCalls     func()
}

func aksMachineAPIHeaderBatchProvisionMode() provisionModeTestCase {
	return provisionModeTestCase{
		name: "AKSMachineAPIHeaderBatch",
		validateNodeClaim: func(nodeClaim *karpv1.NodeClaim) {
			validateAKSMachineNodeClaim(nodeClaim, nodePool)
		},
		resetCreateCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		},
		expectCreateCalls: func() {
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		},
		expectCreatedResource: func() {
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			Expect(createInput.AKSMachine.Properties).ToNot(BeNil())
		},
		resetListCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
			azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
		},
		expectListCalls: func() {
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
		},
		resetGetCalls: func() {
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
		},
		expectGetCalls: func() {
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0))
		},
		resetDeleteCalls: func() {
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
		},
		expectDeleteCalls: func() {
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(0))
		},
	}
}
