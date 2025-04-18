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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
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

var (
	testK8sVersion string
	oldK8sVersion  string
)

var _ = Describe("NodeClass KubernetesVersion Status Controller", func() {
	var nodeClass *v1alpha2.AKSNodeClass
	BeforeEach(func() {
		testK8sVersion = lo.Must(semver.ParseTolerant(lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String())).String()
		semverTestK8sVersion := lo.Must(semver.ParseTolerant(testK8sVersion))
		semverTestK8sVersion.Minor = semverTestK8sVersion.Minor - 1
		oldK8sVersion = semverTestK8sVersion.String()
		nodeClass = test.AKSNodeClass()
	})

	It("Should init KubernetesVersion and its readiness on AKSNodeClass", func() {
		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.KubernetesVersion).To(Equal(testK8sVersion))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeKubernetesVersionReady)).To(BeTrue())
	})

	It("Should update KubernetesVersion when new kubernetes version is detected", func() {
		nodeClass.Status.KubernetesVersion = oldK8sVersion
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

		ExpectApplied(ctx, env.Client, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.KubernetesVersion).To(Equal(oldK8sVersion))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeKubernetesVersionReady)).To(BeTrue())

		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		Expect(nodeClass.Status.KubernetesVersion).To(Equal(testK8sVersion))
		Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeKubernetesVersionReady)).To(BeTrue())
	})

	Context("KubernetesVersionReconciler direct tests", func() {
		var (
			k8sReconciler *status.KubernetesVersionReconciler
		)

		BeforeEach(func() {
			k8sReconciler = status.NewKubernetesVersionReconciler(azureEnv.ImageProvider)
		})

		It("Should update KubernetesVersion when new kubernetes version is detected, and reset node image readiness to false", func() {
			nodeClass.Status.KubernetesVersion = oldK8sVersion
			nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)

			result, err := k8sReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}))

			Expect(nodeClass.Status.KubernetesVersion).To(Equal(testK8sVersion))
			Expect(nodeClass.StatusConditions().IsTrue(v1alpha2.ConditionTypeKubernetesVersionReady)).To(BeTrue())
			Expect(nodeClass.StatusConditions().Get(v1alpha2.ConditionTypeImagesReady).IsFalse()).To(BeTrue())
		})
	})
})
