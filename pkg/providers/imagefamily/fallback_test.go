// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imagefamily

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestCapturedPreferencePreservesStandardCapacity(t *testing.T) {
	image := func(id, arch, gen string) v1beta1.NodeImage {
		return v1beta1.NodeImage{ID: id, Requirements: []corev1.NodeSelectorRequirement{
			{Key: corev1.LabelArchStable, Operator: corev1.NodeSelectorOpIn, Values: []string{arch}},
			{Key: v1beta1.LabelSKUHyperVGeneration, Operator: corev1.NodeSelectorOpIn, Values: []string{gen}},
		}}
	}
	nc := &v1beta1.AKSNodeClass{}
	nc.Status.Images = []v1beta1.NodeImage{image("standard-gen2", "amd64", "2"), image("standard-gen1", "amd64", "1"), image("standard-arm", "arm64", "2")}
	nc.Status.SecurityPatchImages = []v1beta1.NodeImage{image("captured-gen2", "amd64", "2")}
	nc.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
	for _, tc := range []struct{ arch, gen, expected string }{
		{"amd64", "2", "captured-gen2"},
		{"amd64", "1", "standard-gen1"},
		{"arm64", "2", "standard-arm"},
	} {
		t.Run(tc.expected, func(t *testing.T) {
			it := &cloudprovider.InstanceType{Name: tc.expected, Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, tc.arch),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, corev1.NodeSelectorOpIn, tc.gen),
			)}
			got, err := ResolveImageForInstanceType(nc, it)
			if err != nil || got != tc.expected {
				t.Fatalf("got %q, %v; want %q", got, err, tc.expected)
			}
		})
	}
}
