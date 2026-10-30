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
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"

	. "github.com/onsi/gomega"
)

func TestSnapshotRecentlyUsed(t *testing.T) {
	tests := []struct {
		name                 string
		oldKubernetesVersion string
		newKubernetesVersion string
		oldImageVersion      string
		newImageVersion      string
		oldImagesReady       bool
		newImagesReady       bool
		expectSnapshot       bool
	}{
		{
			name:                 "when the image changes",
			oldKubernetesVersion: "1.31.0",
			newKubernetesVersion: "1.31.0",
			oldImageVersion:      "202601.01.0",
			newImageVersion:      "202602.01.0",
			oldImagesReady:       true,
			newImagesReady:       true,
			expectSnapshot:       true,
		},
		{
			name:                 "when the Kubernetes version changes",
			oldKubernetesVersion: "1.31.0",
			newKubernetesVersion: "1.32.0",
			oldImageVersion:      "202601.01.0",
			newImageVersion:      "202601.01.0",
			oldImagesReady:       true,
			newImagesReady:       true,
			expectSnapshot:       true,
		},
		{
			name:                 "when the effective pair does not change",
			oldKubernetesVersion: "1.31.0",
			newKubernetesVersion: "1.31.0",
			oldImageVersion:      "202601.01.0",
			newImageVersion:      "202601.01.0",
			oldImagesReady:       true,
			newImagesReady:       true,
			expectSnapshot:       false,
		},
		{
			name:                 "when the previous images were not ready",
			oldKubernetesVersion: "1.31.0",
			newKubernetesVersion: "1.32.0",
			oldImageVersion:      "202601.01.0",
			newImageVersion:      "202601.01.0",
			oldImagesReady:       false,
			newImagesReady:       true,
			expectSnapshot:       false,
		},
		{
			name:                 "when the new images are not ready",
			oldKubernetesVersion: "1.31.0",
			newKubernetesVersion: "1.32.0",
			oldImageVersion:      "202601.01.0",
			newImageVersion:      "202601.01.0",
			oldImagesReady:       true,
			newImagesReady:       false,
			expectSnapshot:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			oldNodeClass := &v1beta1.AKSNodeClass{
				Status: v1beta1.AKSNodeClassStatus{
					KubernetesVersion: lo.ToPtr(test.oldKubernetesVersion),
					Images:            []v1beta1.NodeImage{{ID: "/gallery/image/versions/" + test.oldImageVersion}},
				},
			}
			oldNodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
			if test.oldImagesReady {
				oldNodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
			}

			newNodeClass := oldNodeClass.DeepCopy()
			newNodeClass.Status.KubernetesVersion = lo.ToPtr(test.newKubernetesVersion)
			newNodeClass.Status.Images[0].ID = "/gallery/image/versions/" + test.newImageVersion
			if test.newImagesReady {
				newNodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
			} else {
				newNodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNotReady", "images are not ready")
			}

			snapshotRecentlyUsed(oldNodeClass, newNodeClass)

			if !test.expectSnapshot {
				g.Expect(newNodeClass.Status.ObservedVersions).To(BeNil())
				return
			}

			g.Expect(newNodeClass.Status.ObservedVersions).ToNot(BeNil())
			g.Expect(newNodeClass.Status.ObservedVersions.RecentlyUsedVersions).To(HaveLen(1))
			snapshot := newNodeClass.Status.ObservedVersions.RecentlyUsedVersions[0]
			g.Expect(snapshot.KubernetesVersion).ToNot(BeNil())
			g.Expect(*snapshot.KubernetesVersion).To(Equal(test.oldKubernetesVersion))
			g.Expect(snapshot.NodeImageVersion).ToNot(BeNil())
			g.Expect(*snapshot.NodeImageVersion).To(Equal(test.oldImageVersion))
		})
	}
}
