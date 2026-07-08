package integration_test

import (
	corev1 "k8s.io/api/core/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Trusted Launch", func() {
	It("should enable vTPM and Secure Boot when explicitly enabled", func() {
		enabled := true
		nodeClass.Spec.Security = &v1beta1.Security{
			TrustedLaunch: &v1beta1.TrustedLaunch{
				VTPM:       &enabled,
				SecureBoot: &enabled,
			},
		}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyTrustedLaunchSettings(node, true, true)
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
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyTrustedLaunchSettings(node, true, false)
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
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyTrustedLaunchSettings(node, false, true)
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
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyTrustedLaunchSettings(node, false, false)
	})

	It("should not enable vTPM or Secure Boot when security is not specified", func() {
		nodeClass.Spec.Security = nil

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.GetNode(pods[0].Spec.NodeName)
		verifyTrustedLaunchSettings(node, false, false)
	})
})

func verifyTrustedLaunchSettings(node *corev1.Node, expectedVTPM, expectedSecureBoot bool) {
	vm := env.GetVM(node.Name)
	Expect(vm.Properties).ToNot(BeNil())

	if expectedVTPM {
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.UefiSettings).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.UefiSettings.VTpmEnabled).ToNot(BeNil())
		Expect(*vm.Properties.SecurityProfile.UefiSettings.VTpmEnabled).To(BeTrue())
	}

	if expectedSecureBoot {
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.UefiSettings).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.UefiSettings.SecureBootEnabled).ToNot(BeNil())
		Expect(*vm.Properties.SecurityProfile.UefiSettings.SecureBootEnabled).To(BeTrue())
	}

	if expectedVTPM || expectedSecureBoot {
		Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
		Expect(vm.Properties.SecurityProfile.SecurityType).ToNot(BeNil())
		Expect(*vm.Properties.SecurityProfile.SecurityType).To(Equal("TrustedLaunch"))
	}
}
