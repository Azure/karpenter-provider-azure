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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	"github.com/samber/lo"

	opstatus "github.com/awslabs/operatorpkg/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

// Test helper functions for expired image testing
func getImageVersionFromDate(date time.Time) string {
	return fmt.Sprintf("%04d%02d.%02d.0", date.Year(), date.Month(), date.Day())
}

func getExpiredImageVersion() string {
	// Create a version that's 91 days old (past the 90-day expiration)
	expiredDate := time.Now().AddDate(0, 0, -91)
	return getImageVersionFromDate(expiredDate)
}

func getRecentImageVersion() string {
	// Create a version that's 30 days old (should not be expired)
	recentDate := time.Now().AddDate(0, 0, -30)
	return getImageVersionFromDate(recentDate)
}

func getTodaysImageVersion() string {
	// Create a version from today
	return getImageVersionFromDate(time.Now())
}

func getTestImagesWithMixedAges() []v1beta1.NodeImage {
	return []v1beta1.NodeImage{
		{
			ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/%s", testSIGSubscriptionID, getExpiredImageVersion()),
			Requirements: []corev1.NodeSelectorRequirement{
				{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"amd64"}},
				{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"2"}},
			},
		},
		{
			ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204containerd/versions/%s", testSIGSubscriptionID, getRecentImageVersion()),
			Requirements: []corev1.NodeSelectorRequirement{
				{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"amd64"}},
				{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"1"}},
			},
		},
		{
			ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2arm64containerd/versions/%s", testSIGSubscriptionID, "malformed.version"),
			Requirements: []corev1.NodeSelectorRequirement{
				{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"arm64"}},
				{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"2"}},
			},
		},
	}
}

const (
	testSIGSubscriptionID = "21098765-4321-4321-4321-210987654321"
)

var (
	// Use recent versions for CIG tests to avoid expiration issues
	oldcigImageVersion = getRecentImageVersion() // 30 days old - won't be expired
	newCIGImageVersion = getTodaysImageVersion() // Today's version - latest available
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

		Context("Expired Image Upgrade Logic", func() {
			var (
				imageReconciler *status.NodeImageReconciler
			)

			BeforeEach(func() {
				os.Setenv("SYSTEM_NAMESPACE", "kube-system")

				// Configure test to use SIG (Shared Image Gallery)
				options := test.Options(test.OptionsFields{
					UseSIG:            lo.ToPtr(true),
					SIGSubscriptionID: lo.ToPtr(testSIGSubscriptionID),
				})
				ctx = options.ToContext(ctx)

				imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface)

				// Set up available current images for the api
				currentImageVersion := getTodaysImageVersion()
				azureEnv.NodeImageVersionsAPI.OverrideNodeImageVersions = []types.NodeImageVersion{
					{FullName: "2204gen2containerd", OS: "AKSUbuntu", SKU: "2204gen2containerd", Version: currentImageVersion},
					{FullName: "2204containerd", OS: "AKSUbuntu", SKU: "2204containerd", Version: currentImageVersion},
					{FullName: "2204gen2arm64containerd", OS: "AKSUbuntu", SKU: "2204gen2arm64containerd", Version: currentImageVersion},
				}
			})

			It("should upgrade expired images but keep recent images when maintenance window is closed", func() {
				// Setup: closed maintenance window
				ExpectApplied(ctx, env.Client, getClosedMWConfigMap())

				// Setup: nodeClass with mix of expired, recent, and malformed images
				nodeClass.Status.Images = getTestImagesWithMixedAges()
				nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				// Verify results with exact expected images
				expectedImages := []v1beta1.NodeImage{
					{
						ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/%s", testSIGSubscriptionID, getTodaysImageVersion()),
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"amd64"}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"2"}},
						},
					},
					{
						ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204containerd/versions/%s", testSIGSubscriptionID, getRecentImageVersion()),
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"amd64"}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"1"}},
						},
					},
					{
						ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2arm64containerd/versions/%s", testSIGSubscriptionID, getTodaysImageVersion()),
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"arm64"}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"2"}},
						},
					},
				}
				Expect(nodeClass.Status.Images).To(Equal(expectedImages))

				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
			})

			It("should not upgrade images when all are recent and maintenance window is closed", func() {
				// Setup: closed maintenance window
				ExpectApplied(ctx, env.Client, getClosedMWConfigMap())

				// Setup: nodeClass with only recent images (all 3 images recent)
				recentVersion := getRecentImageVersion()
				recentImages := []v1beta1.NodeImage{
					{
						ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/%s", testSIGSubscriptionID, recentVersion),
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"amd64"}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"2"}},
						},
					},
					{
						ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204containerd/versions/%s", testSIGSubscriptionID, recentVersion),
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"amd64"}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"1"}},
						},
					},
					{
						ID: fmt.Sprintf("/subscriptions/%s/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2arm64containerd/versions/%s", testSIGSubscriptionID, recentVersion),
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: "In", Values: []string{"arm64"}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: "In", Values: []string{"2"}},
						},
					},
				}
				nodeClass.Status.Images = recentImages
				nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)

				_, err := imageReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				// Verify all recent images remain unchanged (exact same as input)
				Expect(nodeClass.Status.Images).To(Equal(recentImages))
				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
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
