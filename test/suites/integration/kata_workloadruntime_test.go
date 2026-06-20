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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

const (
	kataRuntimeClassName         = "e2e-kata-vm-isolation"
	kataRuntimeNodeSelectorKey   = "kata-runtime-e2e"
	kataRuntimeNodeSelectorValue = "true"
)

func configureKataNodeClass(nodeClass *v1beta1.AKSNodeClass) {
	nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
	nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)
}

func newKataRuntimeClass() *nodev1.RuntimeClass {
	return &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: kataRuntimeClassName,
		},
		Handler: "kata",
		Overhead: &nodev1.Overhead{
			PodFixed: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("600Mi"),
			},
		},
	}
}

var _ = Describe("Kata WorkloadRuntime", func() {
	It("should provision an AzureLinux node for a pod using a test-owned Kata RuntimeClass", func() {
		configureKataNodeClass(nodeClass)
		nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{
			kataRuntimeNodeSelectorKey: kataRuntimeNodeSelectorValue,
		})

		runtimeClass := newKataRuntimeClass()
		DeferCleanup(func() { env.ExpectDeleted(runtimeClass) })

		pod := env.Pod(coretest.PodOptions{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "kata-workloadruntime",
				},
			},
			NodeSelector: map[string]string{
				kataRuntimeNodeSelectorKey: kataRuntimeNodeSelectorValue,
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
		})
		pod.Spec.RuntimeClassName = lo.ToPtr(runtimeClass.Name)
		selector := labels.SelectorFromSet(pod.Labels)

		env.ExpectCreated(runtimeClass, nodeClass, nodePool, pod)

		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyPodCount(selector, 1)

		Expect(nodes[0].Labels).To(HaveKeyWithValue(kataRuntimeNodeSelectorKey, kataRuntimeNodeSelectorValue))
	})
})
