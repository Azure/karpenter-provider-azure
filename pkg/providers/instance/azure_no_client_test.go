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

package instance

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func TestNoAKSMachinesClient_BeginCreateOrUpdate(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	client := NewNoAKSMachinesClient()

	_, err := client.BeginCreateOrUpdate(ctx, "test-rg", "test-cluster", "test-pool", "test-machine", armcontainerservice.Machine{}, nil)

	g.Expect(err).To(HaveOccurred())
	g.Expect(IsAKSMachineOrMachinesPoolNotFound(err)).To(BeTrue())
}

func TestNoAKSMachinesClient_Get(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	client := NewNoAKSMachinesClient()

	_, err := client.Get(ctx, "test-rg", "test-cluster", "test-pool", "test-machine", nil)

	g.Expect(err).To(HaveOccurred())
	g.Expect(IsAKSMachineOrMachinesPoolNotFound(err)).To(BeTrue())
}

func TestNoAKSMachinesClient_NewListPager(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	client := NewNoAKSMachinesClient()

	pager := client.NewListPager("test-rg", "test-cluster", "test-pool", nil)

	g.Expect(pager).ToNot(BeNil())

	g.Expect(pager.More()).To(BeTrue())
	_, err := pager.NextPage(ctx)
	g.Expect(err).To(HaveOccurred())
	g.Expect(IsAKSMachineOrMachinesPoolNotFound(err)).To(BeTrue())
}

func TestNoAKSAgentPoolsClient_Get(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	client := NewNoAKSAgentPoolsClient()

	_, err := client.Get(ctx, "test-rg", "test-cluster", "test-pool", nil)

	g.Expect(err).To(HaveOccurred())
	g.Expect(IsAKSMachineOrMachinesPoolNotFound(err)).To(BeTrue())
}

func TestNoAKSAgentPoolsClient_BeginDeleteMachines(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	client := NewNoAKSAgentPoolsClient()

	_, err := client.BeginDeleteMachines(ctx, "test-rg", "test-cluster", "test-pool", armcontainerservice.AgentPoolDeleteMachinesParameter{
		MachineNames: []*string{
			lo.ToPtr("machine1"),
		},
	}, nil)

	g.Expect(err).To(HaveOccurred())
	g.Expect(IsAKSMachineOrMachinesPoolNotFound(err)).To(BeTrue())
}
