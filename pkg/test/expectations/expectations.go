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

package expectations

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ExpectUnavailable(env *test.Environment, instanceType string, zone string, capacityType string) {
	GinkgoHelper()
	Expect(env.UnavailableOfferingsCache.IsUnavailable(instanceType, zone, capacityType)).To(BeTrue())
}

func ExpectKubeletFlags(env *test.Environment, customData string, expectedFlags map[string]string) {
	GinkgoHelper()
	kubeletFlags := customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
	for flag, value := range expectedFlags {
		Expect(kubeletFlags).To(ContainSubstring(fmt.Sprintf("--%s=%s", flag, value)))
	}
}

func ExpectDecodedCustomData(env *test.Environment) string {
	GinkgoHelper()
	Expect(env.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

	vm := env.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	return decodedString
}
