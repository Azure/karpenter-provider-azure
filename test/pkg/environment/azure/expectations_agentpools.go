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

package azure

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
)

func (env *Environment) ExpectCreatedMachineAgentPool() containerservice.AgentPool {
	GinkgoHelper()
	byoTestMachineAP := containerservice.AgentPool{
		Properties: &containerservice.ManagedClusterAgentPoolProfileProperties{
			Mode: lo.ToPtr(containerservice.AgentPoolModeMachines),
		},
	}
	poller, err := env.agentpoolsClient.BeginCreateOrUpdate(env.Context, env.ClusterResourceGroup, env.ClusterName, env.MachineAgentPoolName, byoTestMachineAP, nil)
	Expect(err).ToNot(HaveOccurred())
	res, err := poller.PollUntilDone(env.Context, nil)
	Expect(err).ToNot(HaveOccurred())
	return res.AgentPool
}
