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
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	imagefamilytypes "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Node image pinning", func() {
	It("should provision a node one Kubernetes version behind the control plane", func() {
		requestedVersion := previousSupportedKubernetesVersion()
		nodeClass.Spec.Versions = &v1beta1.Versions{KubernetesVersion: lo.ToPtr(requestedVersion)}

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.GetKubernetesVersion()).To(Equal(requestedVersion))
		}).Should(Succeed())

		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")).To(Equal(requestedVersion))
	})

	It("should pin the current Kubernetes and node image versions", func() {
		env.ExpectCreated(nodeClass)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.GetKubernetesVersion()).ToNot(BeEmpty())
			g.Expect(nodeClass.GetImages()).ToNot(BeEmpty())
			g.Expect(nodeClass.Status.ObservedVersions).ToNot(BeNil())
			g.Expect(nodeClass.Status.ObservedVersions.LatestImageVersion).ToNot(BeEmpty())
		}).Should(Succeed())

		currentKubernetesVersion := lo.FromPtr(nodeClass.Status.KubernetesVersion)
		currentNodeImageVersion := nodeClass.Status.Images[0].ID[strings.LastIndex(nodeClass.Status.Images[0].ID, "/")+1:]
		Expect(currentNodeImageVersion).To(Equal(lo.FromPtr(nodeClass.Status.ObservedVersions.LatestImageVersion)))
		nodeClass.Spec.Versions = &v1beta1.Versions{
			KubernetesVersion: lo.ToPtr(currentKubernetesVersion),
			NodeImageVersion:  lo.ToPtr(currentNodeImageVersion),
		}
		env.ExpectUpdated(nodeClass)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.Spec.Versions).To(Equal(&v1beta1.Versions{
				KubernetesVersion: lo.ToPtr(currentKubernetesVersion),
				NodeImageVersion:  lo.ToPtr(currentNodeImageVersion),
			}))
			g.Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady).ObservedGeneration).To(Equal(nodeClass.Generation))
			g.Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).ObservedGeneration).To(Equal(nodeClass.Generation))
			g.Expect(nodeClass.GetKubernetesVersion()).To(Equal(currentKubernetesVersion))
			g.Expect(nodeClass.GetImages()).ToNot(BeEmpty())
			g.Expect(nodeClass.Status.Images[0].ID).To(HaveSuffix(currentNodeImageVersion))
		}).Should(Succeed())

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		nodeClaim := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)[0]

		Expect(nodeClaim.Status.ImageID).To(HaveSuffix(currentNodeImageVersion))
		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")).To(Equal(currentKubernetesVersion))
	})

	It("should pin a recently used node image version", func() {
		if env.UsesSharedImageGallery() {
			Skip("requires Community Gallery images")
		}

		env.ExpectCreated(nodeClass)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.GetKubernetesVersion()).ToNot(BeEmpty())
			g.Expect(nodeClass.GetImages()).ToNot(BeEmpty())
			g.Expect(nodeClass.Status.ObservedVersions).ToNot(BeNil())
			g.Expect(nodeClass.Status.ObservedVersions.LatestImageVersion).ToNot(BeEmpty())
		}).Should(Succeed())

		currentKubernetesVersion := lo.FromPtr(nodeClass.Status.KubernetesVersion)
		latestNodeImageVersion := lo.FromPtr(nodeClass.Status.ObservedVersions.LatestImageVersion)
		latestCommunityImageVersion, previousNodeImageVersion := latestCommunityImageVersions(nodeClass.Status.Images[0].ID)
		Expect(latestNodeImageVersion).To(Equal(latestCommunityImageVersion))

		stored := nodeClass.DeepCopy()
		nodeClass.Status.ObservedVersions.RecentlyUsedVersions = []v1beta1.RecentlyUsedVersion{
			{
				KubernetesVersion: lo.ToPtr(currentKubernetesVersion),
				ImageVersion:      lo.ToPtr(previousNodeImageVersion),
			},
		}
		Expect(env.Client.Status().Patch(env.Context, nodeClass, client.MergeFrom(stored))).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(lo.ContainsBy(nodeClass.Status.ObservedVersions.RecentlyUsedVersions, func(version v1beta1.RecentlyUsedVersion) bool {
				return lo.FromPtr(version.KubernetesVersion) == currentKubernetesVersion && lo.FromPtr(version.ImageVersion) == previousNodeImageVersion
			})).To(BeTrue())
		}).Should(Succeed())

		nodeClass.Spec.Versions = &v1beta1.Versions{
			KubernetesVersion: lo.ToPtr(currentKubernetesVersion),
			NodeImageVersion:  lo.ToPtr(previousNodeImageVersion),
		}
		env.ExpectUpdated(nodeClass)

		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
			g.Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady).ObservedGeneration).To(Equal(nodeClass.Generation))
			g.Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).ObservedGeneration).To(Equal(nodeClass.Generation))
			g.Expect(nodeClass.GetKubernetesVersion()).To(Equal(currentKubernetesVersion))
			g.Expect(nodeClass.GetImages()).ToNot(BeEmpty())
			g.Expect(nodeClass.Status.Images[0].ID).To(HaveSuffix(previousNodeImageVersion))
		}).Should(Succeed())

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)
		nodeClaim := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)[0]

		Expect(nodeClaim.Status.ImageID).To(HaveSuffix(previousNodeImageVersion))
		node := env.GetNode(pods[0].Spec.NodeName)
		Expect(strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")).To(Equal(currentKubernetesVersion))
	})
})

func previousSupportedKubernetesVersion() string {
	serverVersion := lo.Must(env.KubeClient.Discovery().ServerVersion())
	controlPlaneVersion := lo.Must(semver.ParseTolerant(serverVersion.GitVersion))
	clientOptions := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{Cloud: env.CloudConfig},
	}
	managedClustersClient := lo.Must(armcontainerservice.NewManagedClustersClient(env.SubscriptionID, env.GetDefaultCredential(), clientOptions))
	response := lo.Must(managedClustersClient.ListKubernetesVersions(env.Context, env.Region, nil))

	var previousVersion semver.Version
	found := false
	for _, version := range response.Values {
		if version == nil {
			continue
		}
		for patchVersion := range version.PatchVersions {
			candidate := lo.Must(semver.Parse(patchVersion))
			if candidate.LT(controlPlaneVersion) && (!found || candidate.GT(previousVersion)) {
				previousVersion = candidate
				found = true
			}
		}
	}
	Expect(found).To(BeTrue(), "expected a supported Kubernetes version before %s", controlPlaneVersion)
	return previousVersion.String()
}

func latestCommunityImageVersions(imageID string) (string, string) {
	imageInfo := imagefamilytypes.DefaultImageOutput{}
	imageInfo.PopulateImageTraitsFromID(imageID)
	Expect(imageInfo.PublicGalleryURL).ToNot(BeEmpty())
	Expect(imageInfo.ImageDefinition).ToNot(BeEmpty())

	clientOptions := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{Cloud: env.CloudConfig},
	}
	versionsClient := lo.Must(armcompute.NewCommunityGalleryImageVersionsClient(env.SubscriptionID, env.GetDefaultCredential(), clientOptions))
	pager := versionsClient.NewListPager(env.Region, imageInfo.PublicGalleryURL, imageInfo.ImageDefinition, nil)

	var versions []*armcompute.CommunityGalleryImageVersion
	for pager.More() {
		page := lo.Must(pager.NextPage(env.Context))
		versions = append(versions, lo.Filter(page.Value, func(version *armcompute.CommunityGalleryImageVersion, _ int) bool {
			return version != nil && version.Name != nil && version.Properties != nil && version.Properties.PublishedDate != nil
		})...)
	}
	Expect(len(versions)).To(BeNumerically(">=", 2))

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Properties.PublishedDate.After(*versions[j].Properties.PublishedDate)
	})
	return lo.FromPtr(versions[0].Name), lo.FromPtr(versions[1].Name)
}
