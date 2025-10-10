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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AKSMachineInstance Helper Functions", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var nodeClaim *karpv1.NodeClaim
	var instanceType *corecloudprovider.InstanceType

	BeforeEach(func() {
		nodeClass = &v1beta1.AKSNodeClass{
			Spec: v1beta1.AKSNodeClassSpec{
				ImageFamily: lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			},
		}
		nodeClaim = &karpv1.NodeClaim{
			Spec: karpv1.NodeClaimSpec{
				Taints: []v1.Taint{
					{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
				},
				StartupTaints: []v1.Taint{
					{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
				},
			},
		}
		instanceType = &corecloudprovider.InstanceType{
			Name: "Standard_D2_v2",
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
			),
		}
	})

	Context("configureOSSKUAndFIPs", func() {
		Context("Ubuntu2204 Image Family", func() {
			BeforeEach(func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
			})

			It("should configure Ubuntu2204 with AMD64 architecture", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2204))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should configure Ubuntu2204 with ARM64 architecture", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2204))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should handle multiple architecture requirements and pick ARM64", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.29.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2204))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should default to AMD64 when no architecture requirement is specified", func() {
				instanceType.Requirements = scheduling.NewRequirements()

				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2204))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})
		})

		Context("AzureLinux Image Family", func() {
			BeforeEach(func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			})

			It("should configure AzureLinux with AMD64 for older Kubernetes version", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUAzureLinux))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should configure AzureLinux with ARM64 for older Kubernetes version", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUAzureLinux))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should configure AzureLinux3 with AMD64 for newer Kubernetes version", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.32.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUAzureLinux))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should configure AzureLinux3 with ARM64 for newer Kubernetes version", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.30.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUAzureLinux))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})
		})

		Context("Generic Ubuntu Image Family with FIPS Mode", func() {
			BeforeEach(func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
				nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
			})

			It("should configure Ubuntu with FIPS mode enabled", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeTrue())
			})

			It("should configure Ubuntu with FIPS mode enabled for ARM64", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeTrue())
			})
		})

		Context("Generic Ubuntu Image Family without FIPS Mode", func() {
			BeforeEach(func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
			})

			It("should configure Ubuntu without FIPS mode for AMD64", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2204))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should configure Ubuntu without FIPS mode for ARM64", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2204))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})

			It("should configure Ubuntu2404 with AMD64 for newer Kubernetes version", func() {
				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "1.34.0")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUUbuntu2404))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})
		})

		Context("Error Cases", func() {
			It("should return error when ImageFamily is nil", func() {
				nodeClass.Spec.ImageFamily = nil

				_, _, err := configureOSSKUAndFIPs(nodeClass, "1.28.0")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ImageFamily is not set"))
			})

			It("should handle empty Kubernetes version gracefully", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)

				ossku, enableFIPs, err := configureOSSKUAndFIPs(nodeClass, "")

				Expect(err).ToNot(HaveOccurred())
				Expect(ossku).ToNot(BeNil())
				Expect(*ossku).To(Equal(armcontainerservice.OSSKUAzureLinux))
				Expect(enableFIPs).ToNot(BeNil())
				Expect(*enableFIPs).To(BeFalse())
			})
		})
	})

	Context("configureTaints", func() {
		Context("Basic Functionality", func() {
			It("should configure taints correctly with startup and regular taints", func() {
				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(2)) // startup-taint + UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(1)) // test-taint

				// Check that UnregisteredNoExecuteTaint is automatically added
				found := false
				for _, taint := range initTaints {
					if *taint == karpv1.UnregisteredNoExecuteTaint.ToString() {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())

				// Check that startup taint is included
				startupTaintFound := false
				for _, taint := range initTaints {
					if *taint == "startup-taint=startup-value:NoExecute" {
						startupTaintFound = true
						break
					}
				}
				Expect(startupTaintFound).To(BeTrue())

				// Check that regular taint is included
				regularTaintFound := false
				for _, taint := range nodeTaints {
					if *taint == "test-taint=test-value:NoSchedule" {
						regularTaintFound = true
						break
					}
				}
				Expect(regularTaintFound).To(BeTrue())
			})

			It("should not duplicate UnregisteredNoExecuteTaint if already present in startup taints", func() {
				// Add UnregisteredNoExecuteTaint to startup taints
				nodeClaim.Spec.StartupTaints = append(nodeClaim.Spec.StartupTaints, karpv1.UnregisteredNoExecuteTaint)

				initTaints, _ := configureTaints(nodeClaim)

				// Should still be 2 (startup-taint + UnregisteredNoExecuteTaint, no duplicate)
				Expect(initTaints).To(HaveLen(2))

				// Count occurrences of UnregisteredNoExecuteTaint
				count := 0
				for _, taint := range initTaints {
					if *taint == karpv1.UnregisteredNoExecuteTaint.ToString() {
						count++
					}
				}
				Expect(count).To(Equal(1))
			})

			It("should not duplicate UnregisteredNoExecuteTaint if already present in regular taints", func() {
				// Add UnregisteredNoExecuteTaint to regular taints
				nodeClaim.Spec.Taints = append(nodeClaim.Spec.Taints, karpv1.UnregisteredNoExecuteTaint)

				initTaints, nodeTaints := configureTaints(nodeClaim)

				// Should be 1 in init taints (startup-taint)
				Expect(initTaints).To(HaveLen(1))
				// Should be 2 in node taints (test-taint + UnregisteredNoExecuteTaint)
				Expect(nodeTaints).To(HaveLen(2))

				// Count total occurrences across both arrays
				totalCount := 0
				for _, taint := range initTaints {
					if *taint == karpv1.UnregisteredNoExecuteTaint.ToString() {
						totalCount++
					}
				}
				for _, taint := range nodeTaints {
					if *taint == karpv1.UnregisteredNoExecuteTaint.ToString() {
						totalCount++
					}
				}
				// Should appear only once total (in init taints, not duplicated)
				Expect(totalCount).To(Equal(1))
			})
		})

		Context("Edge Cases", func() {
			It("should handle empty taints and only add UnregisteredNoExecuteTaint", func() {
				nodeClaim.Spec.Taints = nil
				nodeClaim.Spec.StartupTaints = nil

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(1)) // Only UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(0))
				Expect(*initTaints[0]).To(Equal(karpv1.UnregisteredNoExecuteTaint.ToString()))
			})

			It("should handle empty startup taints but regular taints present", func() {
				nodeClaim.Spec.StartupTaints = nil

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(1)) // Only UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(1)) // Only test-taint
				Expect(*initTaints[0]).To(Equal(karpv1.UnregisteredNoExecuteTaint.ToString()))
			})

			It("should handle empty regular taints but startup taints present", func() {
				nodeClaim.Spec.Taints = nil

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(2)) // startup-taint + UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(0))
			})

			It("should handle multiple startup taints", func() {
				nodeClaim.Spec.StartupTaints = append(nodeClaim.Spec.StartupTaints,
					v1.Taint{Key: "another-startup", Value: "value", Effect: v1.TaintEffectNoExecute})

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(3)) // 2 startup taints + UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(1)) // test-taint
			})

			It("should handle multiple regular taints", func() {
				nodeClaim.Spec.Taints = append(nodeClaim.Spec.Taints,
					v1.Taint{Key: "another-regular", Value: "value", Effect: v1.TaintEffectNoSchedule})

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(2)) // startup-taint + UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(2)) // 2 regular taints
			})

			It("should handle taints with different effects", func() {
				nodeClaim.Spec.Taints = []v1.Taint{
					{Key: "taint1", Value: "value1", Effect: v1.TaintEffectNoSchedule},
					{Key: "taint2", Value: "value2", Effect: v1.TaintEffectNoExecute},
					{Key: "taint3", Value: "value3", Effect: v1.TaintEffectPreferNoSchedule},
				}

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(2)) // startup-taint + UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(3)) // 3 regular taints

				// Verify all taint effects are preserved
				taintStrings := make([]string, len(nodeTaints))
				for i, taint := range nodeTaints {
					taintStrings[i] = *taint
				}
				Expect(taintStrings).To(ContainElement("taint1=value1:NoSchedule"))
				Expect(taintStrings).To(ContainElement("taint2=value2:NoExecute"))
				Expect(taintStrings).To(ContainElement("taint3=value3:PreferNoSchedule"))
			})

			It("should handle taints with empty values", func() {
				nodeClaim.Spec.Taints = []v1.Taint{
					{Key: "empty-value-taint", Value: "", Effect: v1.TaintEffectNoSchedule},
				}
				nodeClaim.Spec.StartupTaints = []v1.Taint{
					{Key: "empty-startup-taint", Value: "", Effect: v1.TaintEffectNoExecute},
				}

				initTaints, nodeTaints := configureTaints(nodeClaim)

				Expect(initTaints).To(HaveLen(2)) // empty-startup-taint + UnregisteredNoExecuteTaint
				Expect(nodeTaints).To(HaveLen(1)) // empty-value-taint

				// Check that empty values are handled correctly
				Expect(*nodeTaints[0]).To(Equal("empty-value-taint:NoSchedule"))
			})
		})
	})

	Context("configureLabelsAndMode", func() {
		BeforeEach(func() {
			nodeClaim.Labels = map[string]string{
				"test-label": "test-value",
			}
		})

		Context("Agent Pool Mode Configuration", func() {
			It("should configure user mode by default", func() {
				labels, mode := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(labels).ToNot(BeNil())
				Expect(mode).ToNot(BeNil())
				Expect(*mode).To(Equal(armcontainerservice.AgentPoolModeUser))

				// Check that capacity type label is added
				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
				Expect(*labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			It("should configure system mode when explicitly specified", func() {
				nodeClaim.Labels["kubernetes.azure.com/mode"] = "system"

				labels, mode := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeSpot)

				Expect(mode).ToNot(BeNil())
				Expect(*mode).To(Equal(armcontainerservice.AgentPoolModeSystem))

				// Check that capacity type label is added
				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
				Expect(*labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeSpot))
			})

			It("should configure user mode when mode label is user", func() {
				nodeClaim.Labels["kubernetes.azure.com/mode"] = "user"

				_, mode := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(mode).ToNot(BeNil())
				Expect(*mode).To(Equal(armcontainerservice.AgentPoolModeUser))
			})

			It("should configure user mode when mode label is empty", func() {
				nodeClaim.Labels["kubernetes.azure.com/mode"] = ""

				_, mode := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(mode).ToNot(BeNil())
				Expect(*mode).To(Equal(armcontainerservice.AgentPoolModeUser))
			})

			It("should configure user mode when mode label has invalid value", func() {
				nodeClaim.Labels["kubernetes.azure.com/mode"] = "invalid-mode"

				_, mode := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(mode).ToNot(BeNil())
				Expect(*mode).To(Equal(armcontainerservice.AgentPoolModeUser))
			})
		})

		Context("Capacity Type Configuration", func() {
			It("should handle on-demand capacity type", func() {
				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
				Expect(*labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			It("should handle spot capacity type", func() {
				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeSpot)

				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
				Expect(*labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeSpot))
			})
		})

		Context("Label Merging and Management", func() {
			It("should include original nodeclaim labels", func() {
				nodeClaim.Labels = map[string]string{
					"custom-label":  "custom-value",
					"another-label": "another-value",
					"environment":   "test",
				}

				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				// Should include original labels
				Expect(labels).To(HaveKey("custom-label"))
				Expect(*labels["custom-label"]).To(Equal("custom-value"))
				Expect(labels).To(HaveKey("another-label"))
				Expect(*labels["another-label"]).To(Equal("another-value"))
				Expect(labels).To(HaveKey("environment"))
				Expect(*labels["environment"]).To(Equal("test"))
			})

			// Note that non-Karpenter system labels population is not being tested here, as it is API's responsibility to do so. Consider adding E2E if we want to validate those.
			// XPMT: TODO(charliedmcb): maybe add unit tests for "sanitization" here, if needed(?)

			It("should handle empty nodeclaim labels", func() {
				nodeClaim.Labels = nil

				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				// Should still have capacity type
				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
				Expect(*labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			It("should handle empty instance type requirements", func() {
				instanceType.Requirements = scheduling.NewRequirements()

				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				// Should include original labels plus capacity type
				Expect(labels).To(HaveKey("test-label"))
				Expect(*labels["test-label"]).To(Equal("test-value"))
				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
				Expect(*labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			It("should handle complex instance type requirements", func() {
				instanceType.Requirements = scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1.LabelInstanceTypeStable, v1.NodeSelectorOpIn, "Standard_D2_v2"),
					scheduling.NewRequirement(v1.LabelTopologyZone, v1.NodeSelectorOpIn, "eastus-1"),
					scheduling.NewRequirement("custom-requirement", v1.NodeSelectorOpIn, "custom-value"),
				)

				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(labels).To(HaveKey("custom-requirement"))
				Expect(*labels["custom-requirement"]).To(Equal("custom-value"))
			})
		})

		Context("Edge Cases", func() {
			It("should handle nil nodeClaim labels map", func() {
				nodeClaim.Labels = nil

				labels, mode := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(labels).ToNot(BeNil())
				Expect(mode).ToNot(BeNil())
				Expect(*mode).To(Equal(armcontainerservice.AgentPoolModeUser))
				Expect(labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
			})

			It("should handle very long label values", func() {
				longValue := strings.Repeat("a", 1000)
				nodeClaim.Labels = map[string]string{
					"long-label": longValue,
				}

				labels, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				Expect(labels).To(HaveKey("long-label"))
				Expect(*labels["long-label"]).To(Equal(longValue))
			})

			It("should preserve label ordering consistency", func() {
				nodeClaim.Labels = map[string]string{
					"z-label": "z-value",
					"a-label": "a-value",
					"m-label": "m-value",
				}

				labels1, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)
				labels2, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

				// Both calls should produce same labels
				Expect(len(labels1)).To(Equal(len(labels2)))
				for key := range labels1 {
					Expect(labels2).To(HaveKey(key))
					Expect(*labels1[key]).To(Equal(*labels2[key]))
				}
			})
		})
	})

	Context("configureKubeletConfig", func() {
		It("should return empty config when nodeClass is nil", func() {
			config := configureKubeletConfig(nil)

			Expect(config).ToNot(BeNil())
			Expect(config.CPUManagerPolicy).To(BeNil())
			Expect(config.CPUCfsQuota).To(BeNil())
			Expect(config.ImageGcHighThreshold).To(BeNil())
			Expect(config.AllowedUnsafeSysctls).To(BeNil())
			Expect(config.ContainerLogMaxSizeMB).To(BeNil())
			Expect(config.PodMaxPids).To(BeNil())
		})

		It("should return empty config when kubelet spec is nil", func() {
			nodeClass.Spec.Kubelet = nil
			config := configureKubeletConfig(nodeClass)

			Expect(config).ToNot(BeNil())
			Expect(config.CPUManagerPolicy).To(BeNil())
		})

		It("should configure all kubelet settings correctly", func() {
			nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
				CPUManagerPolicy:            "static",
				CPUCFSQuota:                 lo.ToPtr(true),
				TopologyManagerPolicy:       "single-numa-node",
				ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
				ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
				AllowedUnsafeSysctls:        []string{"kernel.shm_rmid_forced", "net.core.somaxconn"},
				ContainerLogMaxSize:         "100Mi",
				ContainerLogMaxFiles:        lo.ToPtr(int32(5)),
				PodPidsLimit:                lo.ToPtr(int64(2048)),
			}

			config := configureKubeletConfig(nodeClass)

			Expect(config).ToNot(BeNil())
			Expect(*config.CPUManagerPolicy).To(Equal("static"))
			Expect(*config.CPUCfsQuota).To(BeTrue())
			Expect(*config.TopologyManagerPolicy).To(Equal("single-numa-node"))
			Expect(*config.ImageGcHighThreshold).To(Equal(int32(85)))
			Expect(*config.ImageGcLowThreshold).To(Equal(int32(80)))
			Expect(config.AllowedUnsafeSysctls).To(HaveLen(2))
			Expect(*config.AllowedUnsafeSysctls[0]).To(Equal("kernel.shm_rmid_forced"))
			Expect(config.ContainerLogMaxSizeMB).ToNot(BeNil())
			Expect(*config.ContainerLogMaxFiles).To(Equal(int32(5)))
			Expect(config.PodMaxPids).ToNot(BeNil())
		})

		It("should handle empty/nil values correctly", func() {
			nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
				CPUManagerPolicy:     "",              // Empty string should be nil
				CPUCFSQuota:          lo.ToPtr(false), // False should be preserved
				AllowedUnsafeSysctls: []string{},      // Empty slice should be nil
				ContainerLogMaxSize:  "",              // Empty string should be nil
				ContainerLogMaxFiles: nil,             // Nil should stay nil
				PodPidsLimit:         nil,             // Nil should stay nil
			}

			config := configureKubeletConfig(nodeClass)

			Expect(config.CPUManagerPolicy).To(BeNil())
			Expect(*config.CPUCfsQuota).To(BeFalse())
			Expect(config.AllowedUnsafeSysctls).To(BeNil())
			Expect(config.ContainerLogMaxSizeMB).To(BeNil())
			Expect(config.ContainerLogMaxFiles).To(BeNil())
			Expect(config.PodMaxPids).To(BeNil())
		})
	})

	Context("parseVMImageID", func() {
		Context("Valid Image IDs", func() {
			It("should parse a complete VM image ID correctly", func() {
				vmImageID := "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"

				subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(vmImageID)

				Expect(err).ToNot(HaveOccurred())
				Expect(subscriptionID).To(Equal("10945678-1234-1234-1234-123456789012"))
				Expect(resourceGroup).To(Equal("AKS-Ubuntu"))
				Expect(gallery).To(Equal("AKSUbuntu"))
				Expect(imageName).To(Equal("2204gen2containerd"))
				Expect(version).To(Equal("2022.10.03"))
			})

			It("should parse VM image ID with different values", func() {
				vmImageID := "/subscriptions/abcdef12-3456-7890-abcd-ef1234567890/resourceGroups/MyResourceGroup/providers/Microsoft.Compute/galleries/MyGallery/images/ubuntu20-04/versions/1.0.0"

				subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(vmImageID)

				Expect(err).ToNot(HaveOccurred())
				Expect(subscriptionID).To(Equal("abcdef12-3456-7890-abcd-ef1234567890"))
				Expect(resourceGroup).To(Equal("MyResourceGroup"))
				Expect(gallery).To(Equal("MyGallery"))
				Expect(imageName).To(Equal("ubuntu20-04"))
				Expect(version).To(Equal("1.0.0"))
			})

			It("should handle image ID with hyphens and underscores in names", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg_name/providers/Microsoft.Compute/galleries/test_gallery-name/images/test-image_name/versions/2023.01.15"

				subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(vmImageID)

				Expect(err).ToNot(HaveOccurred())
				Expect(subscriptionID).To(Equal("12345678-1234-1234-1234-123456789012"))
				Expect(resourceGroup).To(Equal("test-rg_name"))
				Expect(gallery).To(Equal("test_gallery-name"))
				Expect(imageName).To(Equal("test-image_name"))
				Expect(version).To(Equal("2023.01.15"))
			})
		})

		Context("Invalid Image IDs", func() {
			It("should return error for empty string", func() {
				_, _, _, _, _, err := parseVMImageID("")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("vmImageID is empty"))
			})

			It("should return error for too few path components", func() {
				vmImageID := "/subscriptions/12345/resourceGroups/test"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected at least 12 parts"))
			})

			It("should return error for incorrect path structure - wrong subscriptions", func() {
				vmImageID := "/subscription/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for incorrect path structure - wrong resourceGroups", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroup/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for incorrect path structure - wrong providers", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/provider/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for incorrect path structure - wrong Microsoft.Compute", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Storage/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for incorrect path structure - wrong galleries", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/gallery/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for incorrect path structure - wrong images", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/image/2204gen2containerd/versions/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for incorrect path structure - wrong versions", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/version/2022.10.03"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected path structure"))
			})

			It("should return error for missing version value", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions"

				_, _, _, _, _, err := parseVMImageID(vmImageID)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("missing version"))
			})

			It("should handle path with trailing slash gracefully", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03/"

				subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(vmImageID)

				Expect(err).ToNot(HaveOccurred())
				Expect(subscriptionID).To(Equal("12345678-1234-1234-1234-123456789012"))
				Expect(resourceGroup).To(Equal("AKS-Ubuntu"))
				Expect(gallery).To(Equal("AKSUbuntu"))
				Expect(imageName).To(Equal("2204gen2containerd"))
				Expect(version).To(Equal("2022.10.03"))
			})
		})

		Context("Edge Cases", func() {
			It("should handle exactly minimum required path length", func() {
				vmImageID := "/subscriptions/a/resourceGroups/b/providers/Microsoft.Compute/galleries/c/images/d/versions/e"

				subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(vmImageID)

				Expect(err).ToNot(HaveOccurred())
				Expect(subscriptionID).To(Equal("a"))
				Expect(resourceGroup).To(Equal("b"))
				Expect(gallery).To(Equal("c"))
				Expect(imageName).To(Equal("d"))
				Expect(version).To(Equal("e"))
			})

			It("should handle path with extra trailing components", func() {
				vmImageID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03/extra/components"

				subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(vmImageID)

				Expect(err).ToNot(HaveOccurred())
				Expect(subscriptionID).To(Equal("12345678-1234-1234-1234-123456789012"))
				Expect(resourceGroup).To(Equal("AKS-Ubuntu"))
				Expect(gallery).To(Equal("AKSUbuntu"))
				Expect(imageName).To(Equal("2204gen2containerd"))
				Expect(version).To(Equal("2022.10.03"))
			})
		})
	})
})
