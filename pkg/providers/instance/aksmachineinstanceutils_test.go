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
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// createAzureResponseError creates a proper Azure SDK error with the given error code and message
func createAzureResponseError(errorCode, errorMessage string, statusCode int) error {
	errorBody := fmt.Sprintf(`{"error": {"code": "%s", "message": "%s"}}`, errorCode, errorMessage)
	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			Body: io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}

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
			creationTime = NewAKSMachineTimestamp()
			aksMachineLocation = "eastus"

			possibleInstanceTypes = []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2_v2",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("7Gi"),
					},
					Requirements: scheduling.NewRequirements(
						scheduling.NewRequirement(v1.LabelInstanceTypeStable, v1.NodeSelectorOpIn, "Standard_D2_v2"),
						scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, "amd64"),
						scheduling.NewRequirement(v1.LabelOSStable, v1.NodeSelectorOpIn, "linux"),
						scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, v1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
					),
					Offerings: corecloudprovider.Offerings{
						{
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, v1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(v1.LabelTopologyZone, v1.NodeSelectorOpIn, "eastus-1"),
							),
							Price:     0.096,
							Available: true,
						},
					},
					Overhead: &corecloudprovider.InstanceTypeOverhead{
						KubeReserved:      v1.ResourceList{},
						SystemReserved:    v1.ResourceList{},
						EvictionThreshold: v1.ResourceList{},
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
						CreationTimestamp: lo.ToPtr(creationTime.Add(10 * time.Minute)),
					},
					NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-202501.28.0"),
					Tags: map[string]*string{
						NodePoolTagKey: lo.ToPtr("test-nodepool"),
						launchtemplate.KarpenterAKSMachineNodeClaimTagKey:         lo.ToPtr("test-nodeclaim"),
						launchtemplate.KarpenterAKSMachineCreationTimestampTagKey: lo.ToPtr(AKSMachineTimestampToTag(creationTime)),
					},
				},
			}
		})

		It("should build NodeClaim successfully from AKS machine", func() {
			nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())
			Expect(nodeClaim.Name).To(Equal("test-nodeclaim"))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
			Expect(nodeClaim.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
			Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal("test-nodepool"))
			Expect(nodeClaim.Labels).To(HaveKey(v1.LabelTopologyZone))
			Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal("eastus-1"))
			Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
			Expect(nodeClaim.CreationTimestamp).To(Equal(metav1.NewTime(creationTime)))
		})

		It("should handle missing zone gracefully", func() {
			aksMachine.Zones = []*string{}

			nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())
			Expect(nodeClaim.Name).To(Equal("test-nodeclaim"))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
			Expect(nodeClaim.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
			Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal("test-nodepool"))
			Expect(nodeClaim.Labels).ToNot(HaveKey(v1.LabelTopologyZone))
			Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
			Expect(nodeClaim.CreationTimestamp).To(Equal(metav1.NewTime(creationTime)))
		})

		It("should handle missing creation time gracefully", func() {
			// Remove the creation timestamp tag to test missing timestamp handling
			delete(aksMachine.Properties.Tags, launchtemplate.KarpenterAKSMachineCreationTimestampTagKey)

			nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())
			Expect(nodeClaim.Name).To(Equal("test-nodeclaim"))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
			Expect(nodeClaim.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
			Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal("test-nodepool"))
			Expect(nodeClaim.Labels).To(HaveKey(v1.LabelTopologyZone))
			Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal("eastus-1"))
			Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
			Expect(nodeClaim.CreationTimestamp).To(Equal(metav1.Time{}))
		})

		It("should return error when properties is missing", func() {
			aksMachine.Properties = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
		})

		It("should return error when VM size is missing", func() {
			aksMachine.Properties.Hardware = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
		})

		It("should return error when priority is missing", func() {
			aksMachine.Properties.Priority = nil

			_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)

			Expect(err).To(HaveOccurred())
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
		It("should return the same name when under length limit", func() {
			nodeClaimName := "default-a1b2c"
			machineName, err := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal(nodeClaimName))
		})

		It("should handle short names correctly", func() {
			nodeClaimName := "d-a1b2c"
			machineName, err := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal(nodeClaimName))
		})

		It("should return the same name when at the length limit", func() {
			nodeClaimName := "123456789-123456789-123456789-a1b2c"
			machineName, err := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal(nodeClaimName))
		})

		It("should truncate and hash when at the length limit +1", func() {
			nodeClaimName := "123456789-123456789-1234567890-a1b2c"
			machineName, err := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(HaveLen(35))
			Expect(machineName).To(HaveSuffix("-a1b2c"))
			Expect(machineName).To(HavePrefix("123456789-123456789-123"))
		})

		It("should truncate and hash differently when at the length limit +1", func() {
			nodeClaimName1 := "123456789-123456789-1234567890-a1b2c"
			nodeClaimName2 := "123456789-123456789-1234567891-a1b2c"
			machineName1, err1 := GetAKSMachineNameFromNodeClaimName(nodeClaimName1)
			machineName2, err2 := GetAKSMachineNameFromNodeClaimName(nodeClaimName2)

			Expect(err1).ToNot(HaveOccurred())
			Expect(err2).ToNot(HaveOccurred())
			Expect(machineName1).ToNot(Equal(machineName2))
		})

		It("should truncate and hash when above the length limit", func() {
			nodeClaimName := "123456789-123456789-123456789-123456789-a1b2c"
			machineName, err := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(HaveLen(35))
			Expect(machineName).To(HaveSuffix("-a1b2c"))
			Expect(machineName).To(HavePrefix("123456789-123456789-123"))
		})

		It("should produce deterministic results for same input", func() {
			nodeClaimName := "consistent-very-long-nodepool-name-test-xyz12"
			machineName1, err1 := GetAKSMachineNameFromNodeClaimName(nodeClaimName)
			machineName2, err2 := GetAKSMachineNameFromNodeClaimName(nodeClaimName)

			Expect(err1).ToNot(HaveOccurred())
			Expect(err2).ToNot(HaveOccurred())
			Expect(machineName1).To(Equal(machineName2))
		})

		It("should preserve the suffix from NodeClaim name", func() {
			longNodeClaimName := "extremely-long-nodepool-name-that-definitely-exceeds-limits-xyz7890"
			machineName, err := GetAKSMachineNameFromNodeClaimName(longNodeClaimName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(HaveSuffix("-xyz7890"))
		})

		It("should handle complex cases correctly", func() {
			nodeClaimName1 := "a-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a1b2c"
			nodeClaimName2 := "a-a-a-a-a-a-a-a-a-a-a-a-a-a-a--a1b2c"
			nodeClaimName3 := "-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a1b2c"
			nodeClaimName4 := "-a-a-a-a-a-a-a-a-a-a-a-a-a-a--a1b2c"
			nodeClaimName5 := "------------------------------a1b2c"
			nodeClaimName6 := "-------------------------------a1b2c"
			machineName1, err1 := GetAKSMachineNameFromNodeClaimName(nodeClaimName1)
			machineName2, err2 := GetAKSMachineNameFromNodeClaimName(nodeClaimName2)
			machineName3, err3 := GetAKSMachineNameFromNodeClaimName(nodeClaimName3)
			machineName4, err4 := GetAKSMachineNameFromNodeClaimName(nodeClaimName4)
			machineName5, err5 := GetAKSMachineNameFromNodeClaimName(nodeClaimName5)
			machineName6, err6 := GetAKSMachineNameFromNodeClaimName(nodeClaimName6)

			Expect(err1).ToNot(HaveOccurred())
			Expect(err2).ToNot(HaveOccurred())
			Expect(err3).ToNot(HaveOccurred())
			Expect(err4).ToNot(HaveOccurred())
			Expect(err5).ToNot(HaveOccurred())
			Expect(err6).ToNot(HaveOccurred())

			Expect(machineName1).To(Equal(nodeClaimName1))
			Expect(machineName4).To(Equal(nodeClaimName4))
			Expect(machineName5).To(Equal(nodeClaimName5))

			Expect(machineName2).To(HaveLen(35))
			Expect(machineName2).To(HaveSuffix("-a1b2c"))
			Expect(machineName2).To(HavePrefix("a-a-a-a-a-a-a-a-a-a-a-a"))

			Expect(machineName3).To(HaveLen(35))
			Expect(machineName3).To(HaveSuffix("-a1b2c"))
			Expect(machineName3).To(HavePrefix("-a-a-a-a-a-a-a-a-a-a-a-"))

			Expect(machineName6).To(HaveLen(35))
			Expect(machineName6).To(HaveSuffix("-a1b2c"))
			Expect(machineName6).To(HavePrefix("-----------------------"))
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
			capacityType := getCapacityTypeFromAKSScaleSetPriority(armcontainerservice.ScaleSetPrioritySpot)
			Expect(capacityType).To(Equal(karpv1.CapacityTypeSpot))
		})

		It("should return on-demand for regular priority", func() {
			capacityType := getCapacityTypeFromAKSScaleSetPriority(armcontainerservice.ScaleSetPriorityRegular)
			Expect(capacityType).To(Equal(karpv1.CapacityTypeOnDemand))
		})
	})

	Context("GetAKSMachineNameFromVMName", func() {
		It("should extract AKS machine name from valid VM name", func() {
			poolName := "aksmanagedap"
			vmName := "aks-aksmanagedap-some-nodepool-a1b2c-12345678-vm"

			machineName, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal("some-nodepool-a1b2c"))
		})

		It("should handle complex machine names with multiple dashes", func() {
			poolName := "aksmanagedap-aks-nodepool-abcde-12345678"
			vmName := "aks-aksmanagedap-aks-nodepool-abcde-12345678-my-complex-machine-name-87654321-vm"

			machineName, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).ToNot(HaveOccurred())
			Expect(machineName).To(Equal("my-complex-machine-name"))
		})

		It("should return error for invalid prefix", func() {
			poolName := "machines"
			vmName := "invalid-prefix-test-machine-123-12345678-vm"

			_, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not start with expected prefix"))
		})

		It("should return error for insufficient parts", func() {
			poolName := "machines"
			vmName := "aks-machines-12345678-vm" // has -vm but no machine name part

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

		It("should return error for unimplemented VMS suffixes", func() {
			poolName := "pool1"
			vmName := "aks-pool1-machine-name-87654321-vm99"

			_, err := GetAKSMachineNameFromVMName(poolName, vmName)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not end with expected suffix"))
		})
	})

	Context("isAKSMachineDeleting", func() {
		It("should return true when provisioning state is Deleting", func() {
			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr("Deleting"),
				},
			}

			result := isAKSMachineDeleting(machine)
			Expect(result).To(BeTrue())
		})

		It("should return false when provisioning state is not Deleting", func() {
			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr("Running"),
				},
			}

			result := isAKSMachineDeleting(machine)
			Expect(result).To(BeFalse())
		})

		It("should return false when machine is nil", func() {
			result := isAKSMachineDeleting(nil)
			Expect(result).To(BeFalse())
		})

		It("should return false when properties is nil", func() {
			machine := &armcontainerservice.Machine{
				Properties: nil,
			}

			result := isAKSMachineDeleting(machine)
			Expect(result).To(BeFalse())
		})

		It("should return false when provisioning state is nil", func() {
			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: nil,
				},
			}

			result := isAKSMachineDeleting(machine)
			Expect(result).To(BeFalse())
		})
	})

	Context("GetAKSLabelZoneFromAKSMachine", func() {
		It("should return zone for AKS machine with single zone", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("1")},
			}
			location := "eastus"

			zone, err := GetAKSLabelZoneFromAKSMachine(machine, location)

			Expect(err).ToNot(HaveOccurred())
			Expect(zone).To(Equal("eastus-1"))
		})

		It("should return empty string for AKS machine with no zones", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{},
			}
			location := "westus2"

			zone, err := GetAKSLabelZoneFromAKSMachine(machine, location)

			Expect(err).ToNot(HaveOccurred())
			Expect(zone).To(Equal(""))
		})

		It("should return empty string for AKS machine with nil zones", func() {
			machine := &armcontainerservice.Machine{
				Zones: nil,
			}
			location := "centralus"

			zone, err := GetAKSLabelZoneFromAKSMachine(machine, location)

			Expect(err).ToNot(HaveOccurred())
			Expect(zone).To(Equal(""))
		})

		It("should return error for nil AKS machine", func() {
			location := "eastus"

			_, err := GetAKSLabelZoneFromAKSMachine(nil, location)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot pass in a nil AKS machine"))
		})

		It("should return error for empty location", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("2")},
			}
			location := ""

			_, err := GetAKSLabelZoneFromAKSMachine(machine, location)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AKS machine is missing location"))
		})

		It("should return error for AKS machine with multiple zones", func() {
			machine := &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("1"), lo.ToPtr("2")},
			}
			location := "eastus"

			_, err := GetAKSLabelZoneFromAKSMachine(machine, location)

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

				zone, err := GetAKSLabelZoneFromAKSMachine(machine, tc.location)

				Expect(err).ToNot(HaveOccurred())
				Expect(zone).To(Equal(tc.expected))
			}
		})
	})

	Context("IsAKSMachineOrMachinesPoolNotFound", func() {
		It("should return false for nil error", func() {
			result := IsAKSMachineOrMachinesPoolNotFound(nil)
			Expect(result).To(BeFalse())
		})

		It("should return true for HTTP 404 status code", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "lol",
				StatusCode:  404,
				RawResponse: nil,
			}

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return true for InvalidParameter error with 'Cannot find any valid machines' message", func() {
			// Create the exact error message from your example
			errorMessage := "Cannot find any valid machines to delete. Please check your input machine names. The valid machines to delete in agent pool 'testmpool' are: testmachine."
			azureError := createAzureResponseError("InvalidParameter", errorMessage, 400)

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeTrue())
		})

		It("should return false for HTTP 400 with InvalidParameter but different message", func() {
			// Create an InvalidParameter error with a different message that shouldn't match
			differentMessage := "InvalidParameter: Some other validation error"
			azureError := createAzureResponseError("InvalidParameter", differentMessage, 400)

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeFalse())
		})

		It("should return false for other HTTP status codes", func() {
			azureError := &azcore.ResponseError{
				ErrorCode:   "Unauthorized",
				StatusCode:  401,
				RawResponse: nil,
			}

			result := IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeFalse())

			azureError = &azcore.ResponseError{
				ErrorCode:   "InternalOperationError",
				StatusCode:  500,
				RawResponse: nil,
			}

			result = IsAKSMachineOrMachinesPoolNotFound(azureError)
			Expect(result).To(BeFalse())
		})

		It("should return false for non-Azure SDK errors", func() {
			result := IsAKSMachineOrMachinesPoolNotFound(fmt.Errorf("some generic error"))
			Expect(result).To(BeFalse())
		})
	})

	Context("BuildJSONFromAKSMachine", func() {
		It("should return empty JSON object for nil machine", func() {
			result := BuildJSONFromAKSMachine(nil)
			Expect(result).To(Equal("{}"))
		})

		It("should return valid JSON for a minimal machine", func() {
			machine := &armcontainerservice.Machine{
				Name: lo.ToPtr("test-machine"),
			}
			result := BuildJSONFromAKSMachine(machine)
			Expect(result).To(ContainSubstring(`"name":"test-machine"`))
		})

		It("should return valid JSON for a fully populated machine", func() {
			machine := &armcontainerservice.Machine{
				Name:  lo.ToPtr("test-machine"),
				Zones: []*string{lo.ToPtr("1")},
				Properties: &armcontainerservice.MachineProperties{
					NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-2023.11.15"),
					Hardware: &armcontainerservice.MachineHardwareProfile{
						VMSize: lo.ToPtr("Standard_D2s_v3"),
					},
					OperatingSystem: &armcontainerservice.MachineOSProfile{
						OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
						OSSKU:        lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
						OSDiskSizeGB: lo.ToPtr(int32(128)),
						OSDiskType:   lo.ToPtr(armcontainerservice.OSDiskTypeManaged),
						EnableFIPS:   lo.ToPtr(false),
					},
					Kubernetes: &armcontainerservice.MachineKubernetesProfile{
						OrchestratorVersion:      lo.ToPtr("1.29.0"),
						MaxPods:                  lo.ToPtr(int32(30)),
						NodeLabels:               map[string]*string{"env": lo.ToPtr("test")},
						NodeInitializationTaints: []*string{lo.ToPtr("node.kubernetes.io/not-ready:NoSchedule")},
					},
					Mode:     lo.ToPtr(armcontainerservice.AgentPoolModeUser),
					Priority: lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
					Security: &armcontainerservice.MachineSecurityProfile{
						EnableEncryptionAtHost: lo.ToPtr(false),
					},
					Tags: map[string]*string{
						"karpenter.sh/nodepool": lo.ToPtr("default"),
					},
				},
			}

			result := BuildJSONFromAKSMachine(machine)

			// Verify it's valid JSON by checking key fields are present
			Expect(result).To(ContainSubstring(`"name":"test-machine"`))
			Expect(result).To(ContainSubstring(`"zones":["1"]`))
			Expect(result).To(ContainSubstring(`"nodeImageVersion":"AKSUbuntu-2204gen2containerd-2023.11.15"`))
			Expect(result).To(ContainSubstring(`"vmSize":"Standard_D2s_v3"`))
			Expect(result).To(ContainSubstring(`"osType":"Linux"`))
			Expect(result).To(ContainSubstring(`"osSKU":"Ubuntu2204"`))
			Expect(result).To(ContainSubstring(`"orchestratorVersion":"1.29.0"`))
			Expect(result).To(ContainSubstring(`"maxPods":30`))
			Expect(result).To(ContainSubstring(`"nodeLabels":{"env":"test"}`))
			Expect(result).To(ContainSubstring(`"nodeInitializationTaints":["node.kubernetes.io/not-ready:NoSchedule"]`))
			Expect(result).To(ContainSubstring(`"mode":"User"`))
			Expect(result).To(ContainSubstring(`"priority":"Regular"`))
		})

		It("should handle machine with empty properties", func() {
			machine := &armcontainerservice.Machine{
				Name:       lo.ToPtr("test-machine"),
				Properties: &armcontainerservice.MachineProperties{},
			}
			result := BuildJSONFromAKSMachine(machine)
			Expect(result).To(ContainSubstring(`"name":"test-machine"`))
			Expect(result).To(ContainSubstring(`"properties":{}`))
		})
	})
})
