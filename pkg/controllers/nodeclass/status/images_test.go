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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

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

func getExpectedTestCommunityImages(version string) []v1alpha2.NodeImage {
	return []v1alpha2.NodeImage{
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/%s", version),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"2"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204containerd/versions/%s", version),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"amd64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
					Operator: "In",
					Values:   []string{"1"},
				},
			},
		},
		{
			ID: fmt.Sprintf("/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2arm64containerd/versions/%s", version),
			Requirements: []corev1.NodeSelectorRequirement{
				{
					Key:      "kubernetes.io/arch",
					Operator: "In",
					Values:   []string{"arm64"},
				},
				{
					Key:      "karpenter.azure.com/sku-hyperv-generation",
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
	var nodeClass *v1alpha2.AKSNodeClass
	BeforeEach(func() {
		var cigImageVersionTest = newCIGImageVersion
		azureEnv.CommunityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &cigImageVersionTest})
		nodeClass = test.AKSNodeClass()
	})

	It("should init Images and its readiness on AKSNodeClass", func() {
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(len(nodeClass.Status.Images)).To(Equal(3))
		Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(newCIGImageVersion)))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
	})

	It("should update Images and its readiness on AKSNodeClass when aksControlPlane is false", func() {
		// Note: when aksControlPlane is false, continuous upgrade is enabled without checking the maintenane windows.
		nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

		ExpectApplied(ctx, env.Client, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())

		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(len(nodeClass.Status.Images)).To(Equal(3))
		Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(newCIGImageVersion)))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
	})

	When("aksControlPlane is true", func() {
		Context("KubernetesVersionReconciler direct tests", func() {
			var (
				imageReconciler *status.NodeImageReconciler
			)

			BeforeEach(func() {
				imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface, true)
			})

			When("SYSTEM_NAMESPACE is set", func() {
				BeforeEach(func() {
					os.Setenv("SYSTEM_NAMESPACE", "kube-system")
				})

				It("Should not update NodeImages when ConfigMap is missing", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})

				It("Should not update NodeImages when maintenane window is not open", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					ExpectApplied(ctx, env.Client, getClosedMWConfigMap())

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})

				It("Should update NodeImages when ConfigMap is empty (maintenane window undefined)", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					ExpectApplied(ctx, env.Client, getEmptyMWConfigMap())

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(newCIGImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})

				It("Should update NodeImages when maintenane window is open", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					ExpectApplied(ctx, env.Client, getOpenMWConfigMap())

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(newCIGImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})

				It("Should error when ConfigMap is malformed (missing endtime)", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					configMap := getOpenMWConfigMap()
					delete(configMap.Data, "aksManagedNodeOSUpgradeSchedule-end")
					ExpectApplied(ctx, env.Client, configMap)

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("unexpected state, with incomplete maintenance window data for channel aksManagedNodeOSUpgradeSchedule"))

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})

				It("Should error when ConfigMap is malformed (invalid timestamp)", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					configMap := getOpenMWConfigMap()
					configMap.Data["aksManagedNodeOSUpgradeSchedule-end"] = "invalid-timestamp"
					ExpectApplied(ctx, env.Client, configMap)

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("error parsing maintenance window end time for channel aksManagedNodeOSUpgradeSchedule"))

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})
			})

			When("SYSTEM_NAMESPACE is not set", func() {
				BeforeEach(func() {
					os.Unsetenv("SYSTEM_NAMESPACE")
				})

				It("Should return an error", func() {
					nodeClass.Status.KubernetesVersion = testK8sVersion
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
					nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeImagesReady)

					_, err := imageReconciler.Reconcile(ctx, nodeClass)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("SYSTEM_NAMESPACE not set"))

					Expect(len(nodeClass.Status.Images)).To(Equal(3))
					Expect(nodeClass.Status.Images).To(HaveExactElements(getExpectedTestCommunityImages(oldcigImageVersion)))
					Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeImagesReady)).To(BeTrue())
				})
			})
		})
	})

	// TODO: Handle test cases where maintenance window is not open, but other update conditions trigger an update, once maintenance windows are supported.
})
