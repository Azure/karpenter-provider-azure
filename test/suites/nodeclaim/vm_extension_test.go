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

package nodeclaim_test

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

		// Skip validation for AKS machine VMs (managed by AKS, not Karpenter directly).
		// The presence of "karpenter.azure.com_aksmachine_nodeclaim" tag indicates this is an AKS machine.
		// Extensions on these VMs are managed by AKS and could change beyond Karpenter's control.
		// The coverage of this case should be handled by other E2Es (e.g., CSE not present would have failed all tests, detailed extension expectations should be tested server-side)
		if vm.Tags != nil {
			if _, isAKSMachine := vm.Tags["karpenter.azure.com_aksmachine_nodeclaim"]; isAKSMachine {
				Skip("Skipping VM extension validation for AKS machine VMs (extensions are managed by AKS)")
			}
		}

		installedExtensions := []string{}
		for _, ext := range vm.Resources {
			installedExtensions = append(installedExtensions, lo.FromPtr(ext.Name))
		}
		expectedExtensions := []any{
			// TODO: Uncomment when AKSLinuxExtension rolls out
			// "AKSLinuxExtension",
			"computeAksLinuxBilling",
		}
		Expect(installedExtensions).To(ContainElements(expectedExtensions...))
		if !env.InClusterController {
			expectedManagedExtensions := []any{
				"cse-agent-karpenter",
			}
			Expect(installedExtensions).To(ContainElements(expectedManagedExtensions))
		}

	})
	//It("should use nodepool tags on the vm extensions karpenter manages", func(){})
})
