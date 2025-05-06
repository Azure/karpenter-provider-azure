package integration_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("VMExtension", func() {
	It("should install all valid vm extensions", func() {
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)

		vm := env.GetVM(pod.Spec.NodeName)
		installedExtensions := []string{}
		for _, ext := range vm.Resources {
			installedExtensions = append(installedExtensions, lo.FromPtr(ext.Name))
		}
		expectedExtensions := []any{
			// TODO: Uncomment when AKSLinuxExtension rolls out
			// "AKSLinuxExtension",
			"computeAksBilling",
		}
		Expect(installedExtensions).To(ConsistOf(expectedExtensions...))

		if !env.InClusterController {
			expectedManagedExtensions := []any{
				"cse-agent-karpenter",
			}
			Expect(installedExtensions).To(ConsistOf(expectedManagedExtensions))
		}

	})
	//It("should use nodepool tags on the vm extensions karpenter manages", func(){})
})
