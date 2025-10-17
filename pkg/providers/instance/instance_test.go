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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestGetPriorityCapacityAndInstanceType(t *testing.T) {
	cases := []struct {
		name                 string
		instanceTypes        []*cloudprovider.InstanceType
		nodeClaim            *karpv1.NodeClaim
		expectedInstanceType string
		expectedPriority     string
		expectedZone         string
	}{
		{
			name:                 "No instance types in the list",
			instanceTypes:        []*cloudprovider.InstanceType{},
			nodeClaim:            &karpv1.NodeClaim{},
			expectedInstanceType: "",
			expectedPriority:     "",
			expectedZone:         "",
		},
		{
			name: "Selects First, Cheapest SKU",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
				{
					Name: "Standard_NV16as_v4",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim:            &karpv1.NodeClaim{},
			expectedInstanceType: "Standard_D2s_v3",
			expectedZone:         "westus-2",
			expectedPriority:     karpv1.CapacityTypeOnDemand,
		},
	}
	provider := NewDefaultProvider(nil, nil, nil, nil, nil, cache.NewUnavailableOfferings(),
		"westus-2",
		"MC_xxxxx_yyyy-region",
		"0000000-0000-0000-0000-0000000000",
		"",
		"", // DiskEncryptionSetID - empty for tests
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			instanceType, priority, zone := provider.pickSkuSizePriorityAndZone(context.TODO(), c.nodeClaim, c.instanceTypes)
			if instanceType != nil {
				assert.Equal(t, c.expectedInstanceType, instanceType.Name)
			}
			assert.Equal(t, c.expectedZone, zone)
			assert.Equal(t, c.expectedPriority, priority)
		})
	}
}

func TestCreateNICFromQueryResponseData(t *testing.T) {
	id := "nic_id"
	name := "nic_name"
	tag := "tag1"
	val := "val1"
	tags := map[string]*string{tag: &val}

	tc := []struct {
		testName      string
		data          map[string]interface{}
		expectedError string
		expectedNIC   *armnetwork.Interface
	}{
		{
			testName: "missing id",
			data: map[string]interface{}{
				"name": name,
			},
			expectedError: "network interface is missing id",
			expectedNIC:   nil,
		},
		{
			testName: "missing name",
			data: map[string]interface{}{
				"id": id,
			},
			expectedError: "network interface is missing name",
			expectedNIC:   nil,
		},
		{
			testName: "happy case",
			data: map[string]interface{}{
				"id":   id,
				"name": name,
				"tags": map[string]interface{}{tag: val},
			},
			expectedNIC: &armnetwork.Interface{
				ID:   &id,
				Name: &name,
				Tags: tags,
			},
		},
	}

	for _, c := range tc {
		nic, err := createNICFromQueryResponseData(c.data)
		if nic != nil {
			expected := *c.expectedNIC
			actual := *nic
			assert.Equal(t, *expected.ID, *actual.ID, c.testName)
			assert.Equal(t, *expected.Name, *actual.Name, c.testName)
			for key := range expected.Tags {
				assert.Equal(t, *(expected.Tags[key]), *(actual.Tags[key]), c.testName)
			}
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}

// Currently tested: ID, Name, Tags, Zones
// TODO: Add the below attributes for Properties if needed:
// Priority, InstanceView.HyperVGeneration, TimeCreated
func TestCreateVMFromQueryResponseData(t *testing.T) {
	id := "vm_id"
	name := "vm_name"
	tag := "tag1"
	val := "val1"
	zone := "us-west"
	tags := map[string]*string{tag: &val}
	zones := []*string{&zone}

	tc := []struct {
		testName      string
		data          map[string]interface{}
		expectedError string
		expectedVM    *armcompute.VirtualMachine
	}{
		{
			testName: "missing id",
			data: map[string]interface{}{
				"name": name,
			},
			expectedError: "virtual machine is missing id",
			expectedVM:    nil,
		},
		{
			testName: "missing name",
			data: map[string]interface{}{
				"id": id,
			},
			expectedError: "virtual machine is missing name",
			expectedVM:    nil,
		},
		{
			testName: "happy case",
			data: map[string]interface{}{
				"id":    id,
				"name":  name,
				"tags":  map[string]interface{}{tag: val},
				"zones": []interface{}{zone},
			},
			expectedVM: &armcompute.VirtualMachine{
				ID:    &id,
				Name:  &name,
				Tags:  tags,
				Zones: zones,
			},
		},
	}

	for _, c := range tc {
		vm, err := createVMFromQueryResponseData(c.data)
		if vm != nil {
			expected := *c.expectedVM
			actual := *vm
			assert.Equal(t, *expected.ID, *actual.ID, c.testName)
			assert.Equal(t, *expected.Name, *actual.Name, c.testName)
			for key := range expected.Tags {
				assert.Equal(t, *(expected.Tags[key]), *(actual.Tags[key]), c.testName)
			}
			for i := range expected.Zones {
				assert.Equal(t, *(expected.Zones[i]), *(actual.Zones[i]), c.testName)
			}
		}
		if err != nil {
			assert.Equal(t, c.expectedError, err.Error(), c.testName)
		}
	}
}
