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

package allocationstrategy_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	. "github.com/onsi/gomega"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestNewInstanceOfferings_CopiesExpectedCollections(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*corecloudprovider.InstanceType{
		{
			Name: "Standard_D2s_v3",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.1, true, karpv1.CapacityTypeOnDemand),
				newOffering(0.2, true, karpv1.CapacityTypeSpot),
			},
		},
	}

	instanceOfferings := allocationstrategy.NewInstanceOfferings(instanceTypes)
	g.Expect(instanceOfferings).To(HaveLen(1))
	g.Expect(instanceOfferings[0].InstanceType).To(BeIdenticalTo(instanceTypes[0]))
	g.Expect(instanceOfferings[0].Offerings).To(HaveLen(2))
	// Pointers are shared (shallow copy)
	g.Expect(instanceOfferings[0].Offerings[0]).To(BeIdenticalTo(instanceTypes[0].Offerings[0]))

	// Reorder the copied slice — the original should be unaffected
	instanceOfferings[0].Offerings[0], instanceOfferings[0].Offerings[1] = instanceOfferings[0].Offerings[1], instanceOfferings[0].Offerings[0]
	g.Expect(instanceOfferings[0].Offerings[0].Price).To(Equal(0.2))
	g.Expect(instanceTypes[0].Offerings[0].Price).To(Equal(0.1))
	g.Expect(instanceTypes[0].Offerings[1].Price).To(Equal(0.2))

	// Truncate the copied slice — the original should be unaffected
	instanceOfferings[0].Offerings = instanceOfferings[0].Offerings[1:]
	g.Expect(instanceOfferings[0].Offerings).To(HaveLen(1))
	g.Expect(instanceTypes[0].Offerings).To(HaveLen(2))
}
