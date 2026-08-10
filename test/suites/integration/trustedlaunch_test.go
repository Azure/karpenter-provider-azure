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

package integration_test

import (
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Trusted Launch", func() {
	It("should enable vTPM and Secure Boot when explicitly enabled for Ubuntu", func() {
		enabled := true
		imageFamily := v1beta1.UbuntuImageFamily
		nodeClass.Spec.ImageFamily = &imageFamily
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &enabled,
				SecureBoot: &enabled,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should enable vTPM and Secure Boot when explicitly enabled for Linux", func() {
		enabled := true
		imageFamily := v1beta1.AzureLinuxImageFamily
		nodeClass.Spec.ImageFamily = &imageFamily
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &enabled,
				SecureBoot: &enabled,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should enable vTPM and Secure Boot when explicitly enabled for Ubuntu 2204", func() {
		enabled := true
		imageFamily := v1beta1.Ubuntu2204ImageFamily
		nodeClass.Spec.ImageFamily = &imageFamily
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &enabled,
				SecureBoot: &enabled,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should enable vTPM and Secure Boot when explicitly enabled for Ubuntu 2404", func() {
		enabled := true
		imageFamily := v1beta1.Ubuntu2404ImageFamily
		nodeClass.Spec.ImageFamily = &imageFamily
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &enabled,
				SecureBoot: &enabled,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should enable vTPM when enabled but not Secure Boot", func() {
		vtpm := true
		secureBoot := false
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &vtpm,
				SecureBoot: &secureBoot,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should enable Secure Boot when enabled but not vTPM", func() {
		vtpm := false
		secureBoot := true
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &vtpm,
				SecureBoot: &secureBoot,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should not enable vTPM or Secure Boot when not enabled", func() {
		disabled := false
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &disabled,
				SecureBoot: &disabled,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		env.EventuallyExpectHealthyDeployment(deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})

	It("should not enable vTPM or Secure Boot when security is not specified", func() {
		nodeClass.Spec.Security = nil

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		verifyTrustedLaunchSettings(nodeClass, node)
	})
})

func verifyTrustedLaunchSettings(nodeClass *v1beta1.AKSNodeClass, node *corev1.Node) {
	vm := env.GetVM(node.Name)
	Expect(vm.Properties).ToNot(BeNil())

	var uefiSettings *armcompute.UefiSettings
	if vm.Properties.SecurityProfile != nil {
		uefiSettings = vm.Properties.SecurityProfile.UefiSettings
	}

	if !nodeClass.IsTrustedLaunchEnabled() {
		expectTrustedLaunchDisabled(vm, uefiSettings)
		return
	}

	Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
	Expect(vm.Properties.SecurityProfile.SecurityType).ToNot(BeNil())
	Expect(*vm.Properties.SecurityProfile.SecurityType).To(Equal(armcompute.SecurityTypesTrustedLaunch))

	Expect(vm.Properties.StorageProfile).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
	Expect(strings.ToLower(utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference))).To(ContainSubstring("tl"))

	if nodeClass.IsVTPMEnabled() {
		Expect(uefiSettings).ToNot(BeNil())
		Expect(uefiSettings.VTpmEnabled).ToNot(BeNil())
		Expect(*uefiSettings.VTpmEnabled).To(BeTrue())
	} else if uefiSettings != nil && uefiSettings.VTpmEnabled != nil {
		Expect(*uefiSettings.VTpmEnabled).To(BeFalse())
	}

	if nodeClass.IsSecureBootEnabled() {
		Expect(uefiSettings).ToNot(BeNil())
		Expect(uefiSettings.SecureBootEnabled).ToNot(BeNil())
		Expect(*uefiSettings.SecureBootEnabled).To(BeTrue())
	} else if uefiSettings != nil && uefiSettings.SecureBootEnabled != nil {
		Expect(*uefiSettings.SecureBootEnabled).To(BeFalse())
	}
}

func expectTrustedLaunchDisabled(vm armcompute.VirtualMachine, uefiSettings *armcompute.UefiSettings) {
	if uefiSettings != nil && uefiSettings.VTpmEnabled != nil {
		Expect(*uefiSettings.VTpmEnabled).To(BeFalse())
	}
	if uefiSettings != nil && uefiSettings.SecureBootEnabled != nil {
		Expect(*uefiSettings.SecureBootEnabled).To(BeFalse())
	}
	if vm.Properties.SecurityProfile != nil && vm.Properties.SecurityProfile.SecurityType != nil {
		Expect(*vm.Properties.SecurityProfile.SecurityType).ToNot(Equal(armcompute.SecurityTypesTrustedLaunch))
	}
	if vm.Properties.StorageProfile != nil && vm.Properties.StorageProfile.ImageReference != nil {
		Expect(strings.ToLower(utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference))).ToNot(ContainSubstring("tl"))
	}
}
