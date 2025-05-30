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
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ExpectUnavailable(env *test.Environment, instanceType, skuFamily, zone, capacityType string, cpuCount int64) {
	GinkgoHelper()
	Expect(env.UnavailableOfferingsCache.IsUnavailable(instanceType, skuFamily, zone, capacityType, cpuCount)).To(BeTrue())
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

// ExpectCleanUp handled the cleanup of all Objects we need within testing that core does not
//
// Core's ExpectCleanedUp function does not currently cleanup ConfigMaps:
// https://github.com/kubernetes-sigs/karpenter/blob/db8df23ffb0b689b116d99597316612c98d382ab/pkg/test/expectations/expectations.go#L244
// TODO: surface this within core and remove this function
func ExpectCleanUp(ctx context.Context, c client.Client) {
	GinkgoHelper()
	wg := sync.WaitGroup{}
	namespaces := &corev1.NamespaceList{}
	Expect(c.List(ctx, namespaces)).To(Succeed())
	for _, object := range []client.Object{
		&corev1.ConfigMap{},
	} {
		for _, namespace := range namespaces.Items {
			wg.Add(1)
			go func(object client.Object, namespace string) {
				GinkgoHelper()
				defer wg.Done()
				defer GinkgoRecover()
				Expect(c.DeleteAllOf(ctx, object, client.InNamespace(namespace),
					&client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: lo.ToPtr(int64(0))}})).ToNot(HaveOccurred())
			}(object, namespace.Name)
		}
	}
	wg.Wait()
}
