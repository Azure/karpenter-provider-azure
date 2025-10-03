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

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

func (env *Environment) ExpectListMachines() []*containerservice.Machine {
	GinkgoHelper()
	var machines []*containerservice.Machine
	pager := env.machinesClient.NewListPager(env.ClusterResourceGroup, env.ClusterName, env.MachineAgentPoolName, nil)
	Expect(pager).ToNot(BeNil())
	for pager.More() {
		page, err := pager.NextPage(env.Context)
		Expect(err).ToNot(HaveOccurred())
		machines = append(machines, page.Value...)
	}

	return machines
}

func (env *Environment) ExpectNoMachines() {
	GinkgoHelper()
	Expect(len(env.ExpectListMachines())).To(Equal(0))
}
