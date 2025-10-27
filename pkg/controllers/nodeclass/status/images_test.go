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

package status_test

import (
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	"github.com/samber/lo"

	opstatus "github.com/awslabs/operatorpkg/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

const (
	oldcigImageVersion = "202410.09.0"
	newCIGImageVersion = "202501.02.0"
)

func getExpectedTestCommunityImages(version string) []v1beta1.NodeImage {
	return []v1beta1.NodeImage{
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/%s", version),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      v1beta1.LabelSKUHyperVGeneration,
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204containerd/versions/%s", version),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      v1beta1.LabelSKUHyperVGeneration,
					Operator: "In",
					Values:   []string{"1"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2arm64containerd/versions/%s", version),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: "In",
					Values:   []string{"arm64"},
				},
				{
					Key:      v1beta1.LabelSKUHyperVGeneration,
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
	}
}

func getClosedMWConfigMap() *corev1.ConfigMap {
	configMap := getEmptyMWConfigMap()
	startTime := time.Now().Add(time.Hour).UTC()
	endTime := time.Now().Add(2 * time.Hour).UTC()
	configMap.Data["aksManagedNodeOSUpgradeSchedule-start"] = startTime.Format(time.RFC3339)
	configMap.Data["aksManagedNodeOSUpgradeSchedule-end"] = endTime.Format(time.RFC3339)
	return configMap
}

func getOpenMWConfigMap() *corev1.ConfigMap {
	configMap := getEmptyMWConfigMap()
	startTime := time.Now().Add(-time.Hour).UTC()
	endTime := time.Now().Add(time.Hour).UTC()
	configMap.Data["aksManagedNodeOSUpgradeSchedule-start"] = startTime.Format(time.RFC3339)
	configMap.Data["aksManagedNodeOSUpgradeSchedule-end"] = endTime.Format(time.RFC3339)
	return configMap
}

func getEmptyMWConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upcoming-maintenance-window",
			Namespace: "kube-system",
		},
		Data: map[string]string{},
	}
}

var _ = Describe("NodeClass NodeImage Status Controller", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		var cigImageVersionTest = newCIGImageVersion
		azureEnv.CommunityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &cigImageVersionTest})
		nodeClass = test.AKSNodeClass()
	})

	It("should init Images and its readiness on AKSNodeClass", func() {
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		ExpectReadyWithCIGImages(nodeClass, newCIGImageVersion)
	})

	It("should update Images and its readiness on AKSNodeClass", func() {
		nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
		nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)

		ExpectApplied(ctx, env.Client, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
		Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())

		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(len(nodeClass.Status.Images)).To(Equal(3))
		Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(newCIGImageVersion)))
		Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
	})

	Context("NodeImageReconciler direct tests", func() {
		BeforeEach(func() {
			// Setup NodeClass
			nodeClass.Status.KubernetesVersion = testK8sVersion
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

			nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
		})

		Context("FIPS Validation With UseSIG", func() {
			var (
				imageReconciler *status.NodeImageReconciler
			)

			BeforeEach(func() {
				imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface)
			})

			It("images ready status should be false if FIPS is enabled but UseSIG is false", func() {
				// set up test options with UseSIG disabled (false)
				options := test.Options(test.OptionsFields{
					UseSIG: lo.ToPtr(false),
				})
				ctx = options.ToContext(ctx)

				nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
				// set ImageFamily to AzureLinux (to bypass unsupported FIPS on the default Ubuntu2204)
				imageFamily := v1beta1.AzureLinuxImageFamily
				nodeClass.Spec.ImageFamily = &imageFamily

				result, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())
				Expect(nodeClass.Status.Images).To(BeNil())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("SIGRequiredForFIPS"))
				Expect(condition.Message).To(Equal("FIPS images require UseSIG to be enabled, but UseSIG is false (note: UseSIG is only supported in AKS managed NAP)"))

				readyCondition := nodeClass.StatusConditions().Get(opstatus.ConditionReady)
				Expect(readyCondition.IsFalse()).To(BeTrue())
			})
		})

		When("SYSTEM_NAMESPACE is set", func() {
			var (
				imageReconciler *status.NodeImageReconciler
			)

			BeforeEach(func() {
				os.Setenv("SYSTEM_NAMESPACE", "kube-system")
				imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface)
			})

			It("Should update NodeImages when ConfigMap is missing (fail open)", func() {
				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				ExpectReadyWithCIGImages(nodeClass, newCIGImageVersion)
			})

			It("Should not update NodeImages when maintenance window is not open", func() {
				ExpectApplied(ctx, env.Client, getClosedMWConfigMap())

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				ExpectReadyWithCIGImages(nodeClass, oldcigImageVersion)
			})

			It("Should update NodeImages when ConfigMap is empty (maintenance window undefined)", func() {
				ExpectApplied(ctx, env.Client, getEmptyMWConfigMap())

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				ExpectReadyWithCIGImages(nodeClass, newCIGImageVersion)
			})

			It("Should update NodeImages when maintenance window is open", func() {
				ExpectApplied(ctx, env.Client, getOpenMWConfigMap())

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				ExpectReadyWithCIGImages(nodeClass, newCIGImageVersion)
			})

			It("Should error when ConfigMap is malformed (missing endtime)", func() {
				configMap := getOpenMWConfigMap()
				delete(configMap.Data, "aksManagedNodeOSUpgradeSchedule-end")
				ExpectApplied(ctx, env.Client, configMap)

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unexpected state, with incomplete maintenance window data for channel aksManagedNodeOSUpgradeSchedule"))

				ExpectReadyWithCIGImages(nodeClass, oldcigImageVersion)
			})

			It("Should error when ConfigMap is malformed (invalid timestamp)", func() {
				configMap := getOpenMWConfigMap()
				configMap.Data["aksManagedNodeOSUpgradeSchedule-end"] = "invalid-timestamp"
				ExpectApplied(ctx, env.Client, configMap)

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("error parsing maintenance window end time for channel aksManagedNodeOSUpgradeSchedule"))

				ExpectReadyWithCIGImages(nodeClass, oldcigImageVersion)
			})
		})

		When("SYSTEM_NAMESPACE is not set", func() {
			var (
				imageReconciler *status.NodeImageReconciler
			)

			BeforeEach(func() {
				os.Unsetenv("SYSTEM_NAMESPACE")
				imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface)
			})

			It("Should update NodeImages (fail open)", func() {
				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				ExpectReadyWithCIGImages(nodeClass, newCIGImageVersion)
			})
		})
	})
})

func ExpectReadyWithCIGImages(nodeClass *v1beta1.AKSNodeClass, version string) {
	GinkgoHelper()

	Expect(len(nodeClass.Status.Images)).To(Equal(3))
	Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(version)))
	Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
}
