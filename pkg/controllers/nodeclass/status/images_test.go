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
	oldcigImageVersion   = "202410.09.0"
	newCIGImageVersion   = "202501.02.0"
	oldSIGImageVersion   = "202410.09.0"
	newSIGImageVersion   = "202608.26.0"
	rollbackImageVersion = "202409.03.0"
	sigSubscriptionID    = "10945678-1234-1234-1234-123456789012"
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

func getExpectedTestSIGImages(version string) []v1beta1.NodeImage {
	nodeClass := test.AKSNodeClass()
	nodeClass.Status.KubernetesVersion = lo.ToPtr(testK8sVersion)
	test.ApplySIGImagesWithVersion(nodeClass, version)
	return nodeClass.Status.Images
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

// getEmptyValuesMWConfigMap returns a ConfigMap with all maintenance window keys present
// but with empty string values, which may be observed under some circumstances
func getEmptyValuesMWConfigMap() *corev1.ConfigMap {
	configMap := getEmptyMWConfigMap()
	configMap.Data["aksManagedAutoUpgradeSchedule-start"] = ""
	configMap.Data["aksManagedAutoUpgradeSchedule-end"] = ""
	configMap.Data["aksManagedNodeOSUpgradeSchedule-start"] = ""
	configMap.Data["aksManagedNodeOSUpgradeSchedule-end"] = ""
	configMap.Data["default-start"] = ""
	configMap.Data["default-end"] = ""
	return configMap
}

var _ = Describe("NodeClass NodeImage Status Controller", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	testCases := []struct {
		name           string
		useSIG         bool
		oldVersion     string
		latestVersion  string
		expectedImages func(string) []v1beta1.NodeImage
	}{
		{
			name:           "CIG",
			oldVersion:     oldcigImageVersion,
			latestVersion:  newCIGImageVersion,
			expectedImages: getExpectedTestCommunityImages,
		},
		{
			name:           "SIG",
			useSIG:         true,
			oldVersion:     oldSIGImageVersion,
			latestVersion:  newSIGImageVersion,
			expectedImages: getExpectedTestSIGImages,
		},
	}

	for _, testCase := range testCases {

		Context("with "+testCase.name, func() {
			BeforeEach(func() {
				if testCase.useSIG {
					ctx = test.Options(test.OptionsFields{
						UseSIG:            lo.ToPtr(true),
						SIGSubscriptionID: lo.ToPtr(sigSubscriptionID),
					}).ToContext(ctx)
					return
				}

				cigImageVersionTest := testCase.latestVersion
				azureEnv.CommunityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &cigImageVersionTest})
			})

			It("should init Images and its readiness on AKSNodeClass", func() {
				ExpectApplied(ctx, env.Client, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)

				ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
			})

			It("should update Images and its readiness on AKSNodeClass", func() {
				nodeClass.Status.Images = testCase.expectedImages(testCase.oldVersion)
				nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)

				ExpectApplied(ctx, env.Client, nodeClass)
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)

				Expect(nodeClass.Status.Images).To(HaveExactElements(testCase.expectedImages(testCase.oldVersion)))
				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())

				ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)

				Expect(len(nodeClass.Status.Images)).To(Equal(3))
				Expect(nodeClass.Status.Images).To(HaveExactElements(testCase.expectedImages(testCase.latestVersion)))
				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
			})

			Context("NodeImageReconciler direct tests", func() {
				BeforeEach(func() {
					// Setup NodeClass
					nodeClass.Status.KubernetesVersion = lo.ToPtr(testK8sVersion)
					nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

					nodeClass.Status.Images = testCase.expectedImages(testCase.oldVersion)
					nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
				})

				When("versions are requested", func() {
					var imageReconciler *status.NodeImageReconciler

					BeforeEach(func() {
						os.Setenv("SYSTEM_NAMESPACE", "kube-system")
						imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface)
						ExpectApplied(ctx, env.Client, getClosedMWConfigMap())

						nodeClass.Status.ObservedVersions = &v1beta1.ObservedVersions{
							CurrentControlPlaneKubernetesVersion: lo.ToPtr(testK8sVersion),
							LatestImageVersion:                   lo.ToPtr(testCase.latestVersion),
						}
					})

					It("should bypass the maintenance window when images are not ready", func() {
						nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "KubernetesVersionChanged", "Kubernetes version changed")
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})

					It("should recover after an invalid image version is corrected", func() {
						nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "NodeImageVersionInvalid", "invalid image version")
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr(testCase.latestVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})

					It("should replace image suffixes with the requested version outside the maintenance window", func() {
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr(testCase.oldVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.oldVersion, testCase.expectedImages)
					})

					It("should immediately return to the latest image when the pin is removed", func() {
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr(testCase.oldVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())
						Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).Reason).To(Equal("NodeImageVersionPinned"))

						nodeClass.Spec.Versions.NodeImageVersion = nil
						_, err = imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
						Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).Reason).NotTo(Equal("NodeImageVersionPinned"))
					})

					It("should reject an image version that was not found in status", func() {
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr("not-found"),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady)
						Expect(condition.IsFalse()).To(BeTrue())
						Expect(condition.Reason).To(Equal("NodeImageVersionInvalid"))
						Expect(nodeClass.Status.Images).To(HaveExactElements(testCase.expectedImages(testCase.oldVersion)))
					})

					It("should reject a malformed image version", func() {
						malformedImageVersion := "invalid/version"
						nodeClass.Status.ObservedVersions.LatestImageVersion = &malformedImageVersion
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr(malformedImageVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady)
						Expect(condition.IsFalse()).To(BeTrue())
						Expect(condition.Reason).To(Equal("RequestedNodeImageVersionUnavailable"))
						Expect(nodeClass.Status.Images).To(HaveExactElements(testCase.expectedImages(testCase.oldVersion)))
					})

					It("should roll back to a recently used image version outside the maintenance window", func() {
						nodeClass.Status.ObservedVersions.RecentlyUsedVersions = []v1beta1.RecentlyUsedVersion{
							{
								NodeImageVersion:  lo.ToPtr(rollbackImageVersion),
								KubernetesVersion: lo.ToPtr(testK8sVersion),
							},
						}
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr(rollbackImageVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, rollbackImageVersion, testCase.expectedImages)
					})

					It("should reject a rollback image paired with a different Kubernetes version", func() {
						nodeClass.Status.ObservedVersions.RecentlyUsedVersions = []v1beta1.RecentlyUsedVersion{
							{
								NodeImageVersion:  lo.ToPtr(rollbackImageVersion),
								KubernetesVersion: lo.ToPtr(oldK8sVersion),
							},
						}
						nodeClass.Spec.Versions = &v1beta1.Versions{
							KubernetesVersion: lo.ToPtr(testK8sVersion),
							NodeImageVersion:  lo.ToPtr(rollbackImageVersion),
						}

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady)
						Expect(condition.IsFalse()).To(BeTrue())
						Expect(condition.Reason).To(Equal("RollbackTargetKubernetesVersionMismatch"))
						Expect(nodeClass.Status.Images).To(HaveExactElements(testCase.expectedImages(testCase.oldVersion)))
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

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})

					It("Should not update NodeImages when maintenance window is not open", func() {
						ExpectApplied(ctx, env.Client, getClosedMWConfigMap())

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.oldVersion, testCase.expectedImages)
					})

					It("Should update NodeImages when ConfigMap is empty (maintenance window undefined)", func() {
						ExpectApplied(ctx, env.Client, getEmptyMWConfigMap())

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})

					It("Should update NodeImages when ConfigMap has keys with empty string values (fail open)", func() {
						ExpectApplied(ctx, env.Client, getEmptyValuesMWConfigMap())

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})

					It("Should update NodeImages when maintenance window is open", func() {
						ExpectApplied(ctx, env.Client, getOpenMWConfigMap())

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).ToNot(HaveOccurred())

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})

					It("Should error when ConfigMap is malformed (missing endtime)", func() {
						configMap := getOpenMWConfigMap()
						delete(configMap.Data, "aksManagedNodeOSUpgradeSchedule-end")
						ExpectApplied(ctx, env.Client, configMap)

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("unexpected state, with incomplete maintenance window data for channel aksManagedNodeOSUpgradeSchedule"))

						ExpectReadyWithImages(nodeClass, testCase.oldVersion, testCase.expectedImages)
					})

					It("Should error when ConfigMap is malformed (invalid timestamp)", func() {
						configMap := getOpenMWConfigMap()
						configMap.Data["aksManagedNodeOSUpgradeSchedule-end"] = "invalid-timestamp"
						ExpectApplied(ctx, env.Client, configMap)

						_, err := imageReconciler.Reconcile(ctx, nodeClass)
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("error parsing maintenance window end time for channel aksManagedNodeOSUpgradeSchedule"))

						ExpectReadyWithImages(nodeClass, testCase.oldVersion, testCase.expectedImages)
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

						ExpectReadyWithImages(nodeClass, testCase.latestVersion, testCase.expectedImages)
					})
				})
			})
		})
	}

	Context("FIPS Validation With UseSIG", func() {
		var imageReconciler *status.NodeImageReconciler

		BeforeEach(func() {
			ctx = test.Options(test.OptionsFields{
				UseSIG: lo.ToPtr(false),
			}).ToContext(ctx)

			nodeClass.Status.KubernetesVersion = lo.ToPtr(testK8sVersion)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
			nodeClass.Status.Images = getExpectedTestCommunityImages(oldcigImageVersion)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
			imageReconciler = status.NewNodeImageReconciler(azureEnv.ImageProvider, env.KubernetesInterface)
		})

		It("images ready status should be false if FIPS is enabled but UseSIG is false", func() {
			nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
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
})

func ExpectReadyWithImages(nodeClass *v1beta1.AKSNodeClass, version string, expectedImages func(string) []v1beta1.NodeImage) {
	GinkgoHelper()

	Expect(nodeClass.Status.Images).To(HaveExactElements(expectedImages(version)))
	Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeImagesReady)).To(BeTrue())
}
