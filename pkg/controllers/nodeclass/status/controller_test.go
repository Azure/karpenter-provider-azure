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

package status

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Recently Used Versions", func() {
	DescribeTable("snapshots the previous effective pair",
		func(oldKubernetesVersion, newKubernetesVersion, oldImageVersion, newImageVersion string, oldImagesReady, newImagesReady, expectSnapshot bool) {
			oldNodeClass := &v1beta1.AKSNodeClass{
				Status: v1beta1.AKSNodeClassStatus{
					KubernetesVersion: lo.ToPtr(oldKubernetesVersion),
					Images:            []v1beta1.NodeImage{{ID: "/gallery/image/versions/" + oldImageVersion}},
				},
			}
			oldNodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
			if oldImagesReady {
				oldNodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
			}

			newNodeClass := oldNodeClass.DeepCopy()
			newNodeClass.Status.KubernetesVersion = lo.ToPtr(newKubernetesVersion)
			newNodeClass.Status.Images[0].ID = "/gallery/image/versions/" + newImageVersion
			if newImagesReady {
				newNodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
			} else {
				newNodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNotReady", "images are not ready")
			}

			snapshotRecentlyUsed(oldNodeClass, newNodeClass)

			if !expectSnapshot {
				Expect(newNodeClass.Status.ObservedVersions).To(BeNil())
				return
			}
			Expect(newNodeClass.Status.ObservedVersions.RecentlyUsedVersions).To(HaveLen(1))
			Expect(newNodeClass.Status.ObservedVersions.RecentlyUsedVersions[0].KubernetesVersion).To(Equal(lo.ToPtr(oldKubernetesVersion)))
			Expect(newNodeClass.Status.ObservedVersions.RecentlyUsedVersions[0].ImageVersion).To(Equal(lo.ToPtr(oldImageVersion)))
		},
		Entry("when the image changes", "1.31.0", "1.31.0", "202601.01.0", "202602.01.0", true, true, true),
		Entry("when the Kubernetes version changes", "1.31.0", "1.32.0", "202601.01.0", "202601.01.0", true, true, true),
		Entry("when the effective pair does not change", "1.31.0", "1.31.0", "202601.01.0", "202601.01.0", true, true, false),
		Entry("when the previous images were not ready", "1.31.0", "1.32.0", "202601.01.0", "202601.01.0", false, true, false),
		Entry("when the new images are not ready", "1.31.0", "1.32.0", "202601.01.0", "202601.01.0", true, false, true),
	)
})
