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
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

type testKubernetesVersionProvider struct {
	controlPlaneVersion string
	supportedVersions   map[string]bool
}

func (p *testKubernetesVersionProvider) KubeServerVersion(context.Context) (string, error) {
	return p.controlPlaneVersion, nil
}

func (p *testKubernetesVersionProvider) IsSupported(_ context.Context, version string) (bool, error) {
	return p.supportedVersions[version], nil
}

var _ = Describe("NodeClass KubernetesVersion Status Controller", func() {
	var nodeClass *v1beta1.AKSNodeClass
	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("Should init KubernetesVersion and its readiness on AKSNodeClass", func() {
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.KubernetesVersion).NotTo(BeNil())
		Expect(*nodeClass.Status.KubernetesVersion).To(Equal(testK8sVersion))
		Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
	})

	It("Should update KubernetesVersion when new kubernetes version is detected", func() {
		nodeClass.Status.KubernetesVersion = lo.ToPtr(oldK8sVersion)
		nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

		ExpectApplied(ctx, env.Client, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.KubernetesVersion).NotTo(BeNil())
		Expect(*nodeClass.Status.KubernetesVersion).To(Equal(oldK8sVersion))
		Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())

		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.KubernetesVersion).NotTo(BeNil())
		Expect(*nodeClass.Status.KubernetesVersion).To(Equal(testK8sVersion))
		Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
	})

	Context("KubernetesVersionReconciler direct tests", func() {
		var (
			k8sReconciler *status.KubernetesVersionReconciler
		)

		BeforeEach(func() {
			k8sReconciler = status.NewKubernetesVersionReconciler(azureEnv.KubernetesVersionProvider)
		})

		It("Should update KubernetesVersion when new kubernetes version is detected, and reset node image readiness to false", func() {
			nodeClass.Status.KubernetesVersion = lo.ToPtr(oldK8sVersion)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)

			result, err := k8sReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}))

			Expect(nodeClass.Status.KubernetesVersion).NotTo(BeNil())
			Expect(*nodeClass.Status.KubernetesVersion).To(Equal(testK8sVersion))
			Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).IsFalse()).To(BeTrue())
		})

		When("a Kubernetes version is requested", func() {
			BeforeEach(func() {
				k8sReconciler = status.NewKubernetesVersionReconciler(&testKubernetesVersionProvider{
					controlPlaneVersion: testK8sVersion,
					supportedVersions: map[string]bool{
						oldK8sVersion: true,
					},
				})
			})

			It("should publish the requested version before images resolve", func() {
				nodeClass.Status.KubernetesVersion = lo.ToPtr(testK8sVersion)
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr(oldK8sVersion),
				}

				result, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}))

				Expect(nodeClass.Status.KubernetesVersion).To(Equal(lo.ToPtr(oldK8sVersion)))
				Expect(nodeClass.Status.ObservedVersions.ControlPlaneKubernetesVersion).To(Equal(lo.ToPtr(testK8sVersion)))
				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).IsFalse()).To(BeTrue())
			})

			It("should reject an invalid version format", func() {
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr("invalid"),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("KubernetesVersionInvalidFormat"))
			})

			It("should recover after an invalid version format is corrected", func() {
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionInvalidFormat", "invalid version format")
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr(oldK8sVersion),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
			})

			It("should reject a version incompatible with the control plane", func() {
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr("2.0.0"),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("KubernetesVersionControlPlaneIncompatible"))
			})

			It("should reject a version more than three minors behind the control plane", func() {
				tooOldVersion := lo.Must(semver.Parse(testK8sVersion))
				tooOldVersion.Minor -= 4
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr(tooOldVersion.String()),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("KubernetesVersionControlPlaneIncompatible"))
				Expect(nodeClass.Status.KubernetesVersion).To(BeNil())
			})

			It("should accept a version exactly three minors behind the control plane", func() {
				oldestCompatibleVersion := lo.Must(semver.Parse(testK8sVersion))
				oldestCompatibleVersion.Minor -= 3
				requestedVersion := oldestCompatibleVersion.String()
				k8sReconciler = status.NewKubernetesVersionReconciler(&testKubernetesVersionProvider{
					controlPlaneVersion: testK8sVersion,
					supportedVersions:   map[string]bool{requestedVersion: true},
				})
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr(requestedVersion),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				Expect(nodeClass.StatusConditions().IsTrue(v1beta1.ConditionTypeKubernetesVersionReady)).To(BeTrue())
				Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).IsFalse()).To(BeTrue())
			})

			It("should reject a newer patch version on the control-plane minor", func() {
				newerPatchVersion := lo.Must(semver.Parse(testK8sVersion))
				newerPatchVersion.Patch++
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr(newerPatchVersion.String()),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("KubernetesVersionControlPlaneIncompatible"))
				Expect(nodeClass.Status.KubernetesVersion).To(BeNil())
			})

			It("should reject an unsupported version", func() {
				nodeClass.Spec.Versions = &v1beta1.Versions{
					KubernetesVersion: lo.ToPtr(testK8sVersion),
				}

				_, err := k8sReconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal("KubernetesVersionUnsupported"))
			})
		})
	})
})
