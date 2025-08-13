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
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AKSMachineInstanceUtils Helper Functions", func() {

	Context("BuildNodeClaimFromAKSMachine", func() {
		var (
			ctx                   context.Context
			aksMachine            *armcontainerservice.Machine
			possibleInstanceTypes []*corecloudprovider.InstanceType
			aksMachineLocation    string
			creationTime          time.Time
		)

		BeforeEach(func() {
			ctx = context.Background()
			creationTime = time.Now()
			aksMachineLocation = "eastus"

			possibleInstanceTypes = []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2_v2",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("7Gi"),
					},
				},
			}

			aksMachine = &armcontainerservice.Machine{
				ID:    lo.ToPtr("/subscriptions/test/resourceGroups/test/providers/Microsoft.ContainerService/managedClusters/test/agentPools/pool/machines/test-machine"),
				Name:  lo.ToPtr("test-machine"),
				Zones: []*string{lo.ToPtr("1")},
				Properties: &armcontainerservice.MachineProperties{
					Hardware: &armcontainerservice.MachineHardwareProfile{
						VMSize: lo.ToPtr("Standard_D2_v2"),
					},
					Priority:   lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
					ResourceID: lo.ToPtr("/subscriptions/test/resourceGroups/test/providers/Microsoft.Compute/virtualMachines/test-vm"),
					Status: &armcontainerservice.MachineStatus{
						CreationTimestamp: &creationTime,
					},
					NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-202501.28.0"),
					Tags: map[string]*string{
						NodePoolTagKey: lo.ToPtr("test-nodepool"),
					},
				},
			}
		})

		It("should build NodeClaim successfully from AKS machine", func() {
			nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())
			Expect(nodeClaim.Name).To(Equal("test-machine"))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
			Expect(nodeClaim.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
			Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal("test-nodepool"))
			Expect(nodeClaim.Labels).To(HaveKey(v1.LabelTopologyZone))
			Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal("eastus-1"))
			Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
		})

		It("should handle missing zone gracefully", func() {
			aksMachine.Zones = []*string{}

			nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())
			Expect(nodeClaim.Name).To(Equal("test-machine"))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
			Expect(nodeClaim.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
			Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal("test-nodepool"))
			Expect(nodeClaim.Labels).ToNot(HaveKey(v1.LabelTopologyZone))
			Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
		})

		It("should return error when machine name is missing", func() {
			aksMachine.Name = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing name"))
		})

		It("should return error when properties is missing", func() {
			aksMachine.Properties = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing Properties"))
		})

		It("should return error when VM size is missing", func() {
			aksMachine.Properties.Hardware = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing VMSize"))
		})

		It("should return error when priority is missing", func() {
			aksMachine.Properties.Priority = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing Priority"))
		})
	})

	Context("FindNodePoolFromAKSMachine", func() {
		var (
			ctx        context.Context
			aksMachine *armcontainerservice.Machine
		)

		BeforeEach(func() {
			ctx = context.Background()

			aksMachine = &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{
						"karpenter.sh_nodepool": lo.ToPtr("test-nodepool"),
					},
				},
			}
		})

		It("should find NodePool when tag exists and NodePool exists", func() {
			nodePool := &karpv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodepool",
				},
			}

			client := fake.NewClientBuilder().WithObjects(nodePool).Build()
			foundNodePool, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)

			Expect(err).ToNot(HaveOccurred())
			Expect(foundNodePool).ToNot(BeNil())
			Expect(foundNodePool.Name).To(Equal("test-nodepool"))
		})

		It("should return NotFound error when NodePool tag is missing", func() {
			aksMachine.Properties.Tags = map[string]*string{}
			client := fake.NewClientBuilder().Build()

			_, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)

			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should return NotFound error when NodePool tag is empty", func() {
			aksMachine.Properties.Tags["karpenter.sh_nodepool"] = lo.ToPtr("")
			client := fake.NewClientBuilder().Build()

			_, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)

			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should return NotFound error when NodePool does not exist in cluster", func() {
			// Create client without the NodePool
			emptyClient := fake.NewClientBuilder().Build()

			_, err := FindNodePoolFromAKSMachine(ctx, aksMachine, emptyClient)

			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("GetAKSMachineNameFromNodeClaimName", func() {
		It("should return the same name", func() {
			nodeClaimName := "test-nodeclaim-123"
			machineName := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(machineName).To(Equal(nodeClaimName))
		})

		It("should handle empty string", func() {
			nodeClaimName := ""
			machineName := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(machineName).To(Equal(""))
		})
	})

	Context("GetNodeClaimNameFromAKSMachineName", func() {
		It("should return the same name", func() {
			aksMachineName := "test-machine-456"
			nodeClaimName := GetNodeClaimNameFromAKSMachineName(aksMachineName)

			Expect(nodeClaimName).To(Equal(aksMachineName))
		})

		It("should handle empty string", func() {
			aksMachineName := ""
			nodeClaimName := GetNodeClaimNameFromAKSMachineName(aksMachineName)

			Expect(nodeClaimName).To(Equal(""))
		})
	})

	Context("GetAKSMachineNameFromNodeClaim", func() {
		It("should return AKS machine name when annotation exists", func() {
			nodeClaim := &karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						v1beta1.AnnotationAKSMachineResourceID: "/subscriptions/test/resourceGroups/test/providers/Microsoft.ContainerService/managedClusters/test/agentPools/aksmanagedap/machines/default-a1b2c3",
					},
				},
			}

			machineName, isAKSMachine := GetAKSMachineNameFromNodeClaim(nodeClaim)

			Expect(isAKSMachine).To(BeTrue())
			Expect(machineName).To(Equal("default-a1b2c3"))
		})

		It("should return false when annotation is missing", func() {
			nodeClaim := &karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			}

			_, isAKSMachine := GetAKSMachineNameFromNodeClaim(nodeClaim)

			Expect(isAKSMachine).To(BeFalse())
		})

		It("should return false when annotations is nil", func() {
			nodeClaim := &karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			}

			_, isAKSMachine := GetAKSMachineNameFromNodeClaim(nodeClaim)

			Expect(isAKSMachine).To(BeFalse())
		})

		It("should handle resource ID with different format", func() {
			nodeClaim := &karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						v1beta1.AnnotationAKSMachineResourceID: "/different/path/to/machine-name-123",
					},
				},
			}

			machineName, isAKSMachine := GetAKSMachineNameFromNodeClaim(nodeClaim)

			Expect(isAKSMachine).To(BeTrue())
			Expect(machineName).To(Equal("machine-name-123"))
		})
	})

	Context("GetCapacityTypeFromAKSScaleSetPriority", func() {
		It("should return spot for spot priority", func() {
			capacityType := GetCapacityTypeFromAKSScaleSetPriority(armcontainerservice.ScaleSetPrioritySpot)
			Expect(capacityType).To(Equal(karpv1.CapacityTypeSpot))
		})

		It("should return on-demand for regular priority", func() {
			capacityType := GetCapacityTypeFromAKSScaleSetPriority(armcontainerservice.ScaleSetPriorityRegular)
			Expect(capacityType).To(Equal(karpv1.CapacityTypeOnDemand))
		})
	})

	Context("GetAKSMachineNameFromVMName", func() {
		It("should extract AKS machine name from valid VM name", func() {
			poolName := "aksmanagedap"
			vmName := "aks-aksmanagedap-some-nodepool-a1b2c-12345678-vms0"

			machineName, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal("some-nodepool-a1b2c"))
		})

		It("should handle complex machine names with multiple dashes", func() {
			poolName := "aksmanagedap-aks-nodepool-abcde-12345678"
			vmName := "aks-aksmanagedap-aks-nodepool-abcde-12345678-my-complex-machine-name-87654321-vms1"

			machineName, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal("my-complex-machine-name"))
		})

		It("should return error for invalid prefix", func() {
			poolName := "machines"
			vmName := "invalid-prefix-test-machine-123-12345678-vms0"

			_, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not start with expected prefix"))
		})

		It("should return error for insufficient parts", func() {
			poolName := "machines"
			vmName := "aks-machines-a1b2c"

			_, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not have enough parts"))
		})

		It("should return error for invalid suffix", func() {
			poolName := "machines"
			vmName := "aks-machines-test-machine-123-12345678-invalid"

			_, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not end with expected suffix"))
		})

		It("should handle different VMS suffixes", func() {
			poolName := "pool1"
			vmName := "aks-pool1-machine-name-87654321-vms99"

			machineName, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal("machine-name"))
		})
	})

	Context("IsAKSMachineDeleting", func() {
		// XPMT: TODO: check this one the API is available
		It("should return true when provisioning state is Deleting", func() {
			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr("Deleting"),
				},
			}

			result := IsAKSMachineDeleting(machine)
			Expect(result).To(BeTrue())
		})

		It("should return false when provisioning state is not Deleting", func() {
			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr("Running"),
				},
			}

			result := IsAKSMachineDeleting(machine)
			Expect(result).To(BeFalse())
		})

		It("should return false when machine is nil", func() {
			result := IsAKSMachineDeleting(nil)
			Expect(result).To(BeFalse())
		})

		It("should return false when properties is nil", func() {
			machine := &armcontainerservice.Machine{
				Properties: nil,
			}

			result := IsAKSMachineDeleting(machine)
			Expect(result).To(BeFalse())
		})

		It("should return false when provisioning state is nil", func() {
			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: nil,
				},
			}

			result := IsAKSMachineDeleting(machine)
			Expect(result).To(BeFalse())
		})
	})

	Context("GetAKSZoneFromAKSMachine", func() {
		It("should return zone for AKS machine with single zone", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("1")},
			}
			location := "eastus"

			zone, err := GetAKSZoneFromAKSMachine(machine, location)

			Expect(err).ToNot(HaveOccurred())
			Expect(zone).To(Equal("eastus-1"))
		})

		It("should return empty string for AKS machine with no zones", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{},
			}
			location := "westus2"

			zone, err := GetAKSZoneFromAKSMachine(machine, location)

			Expect(err).ToNot(HaveOccurred())
			Expect(zone).To(Equal(""))
		})

		It("should return empty string for AKS machine with nil zones", func() {
			machine := &armcontainerservice.Machine{
				Zones: nil,
			}
			location := "centralus"

			zone, err := GetAKSZoneFromAKSMachine(machine, location)

			Expect(err).ToNot(HaveOccurred())
			Expect(zone).To(Equal(""))
		})

		It("should return error for nil AKS machine", func() {
			location := "eastus"

			_, err := GetAKSZoneFromAKSMachine(nil, location)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot pass in a nil AKS machine"))
		})

		It("should return error for empty location", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("2")},
			}
			location := ""

			_, err := GetAKSZoneFromAKSMachine(machine, location)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AKS machine is missing location"))
		})

		It("should return error for AKS machine with multiple zones", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("1"), lo.ToPtr("2")},
			}
			location := "eastus"

			_, err := GetAKSZoneFromAKSMachine(machine, location)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AKS machine has multiple zones"))
		})

		It("should handle different zones correctly", func() {
			testCases := []struct {
				zoneID   string
				location string
				expected string
			}{
				{"1", "eastus", "eastus-1"},
				{"2", "westus2", "westus2-2"},
				{"3", "centralus", "centralus-3"},
			}

			for _, tc := range testCases {
				machine := &armcontainerservice.Machine{
					Zones: []*string{lo.ToPtr(tc.zoneID)},
				}

				zone, err := GetAKSZoneFromAKSMachine(machine, tc.location)

				Expect(err).ToNot(HaveOccurred())
				Expect(zone).To(Equal(tc.expected))
			}
		})
	})

	Context("IsARMNotFound", func() {
		// XPMT: TODO: check this one the API is available
		It("should return false for nil error", func() {
			result := IsARMNotFound(nil)
			Expect(result).To(BeFalse())
		})

		It("should return true for Azure SDK ResourceGroupNotFound error", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "ResourceGroupNotFound",
				StatusCode:  404,
				RawResponse: nil,
			}

			result := IsARMNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return true for Azure SDK ResourceNotFound error", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "ResourceNotFound", // ManagedCluster not found have this
				StatusCode:  404,
				RawResponse: nil,
			}

			result := IsARMNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return true for Azure SDK NotFound error code", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "NotFound", // AgentPool not found have this
				StatusCode:  404,
				RawResponse: nil,
			}

			result := IsARMNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return false for other Azure SDK errors", func() {
			// Test with a different Azure error that should not be treated as "not found"
			azureError := &azcore.ResponseError{
				ErrorCode:   "Unauthorized",
				StatusCode:  401,
				RawResponse: nil,
			}

			result := IsARMNotFound(azureError)
			Expect(result).To(BeFalse())
		})

		It("should return false for non-Azure SDK errors", func() {
			// Test with a generic Go error
			result := IsARMNotFound(fmt.Errorf("some generic error"))
			Expect(result).To(BeFalse())
		})
	})
})
