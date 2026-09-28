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

package cloudprovider

import (
	"context"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestCapturedNodeDoesNotDriftWhenCatalogDisappears(t *testing.T) {
	claim := &karpv1.NodeClaim{}
	claim.Status.ImageID = "AKSUbuntu-2404gen2containerd-202605.27.0-2026.07.18"
	nodeClass := &v1beta1.AKSNodeClass{}
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
	nodeClass.Status.Images = []v1beta1.NodeImage{{ID: "/images/2404gen2containerd/versions/202609.01.0"}}
	reason, err := (&CloudProvider{}).isImageVersionDrifted(context.Background(), claim, nodeClass)
	if err != nil || reason != "" {
		t.Fatalf("unexpected downgrade drift: %s, %v", reason, err)
	}
}

func TestSecurityPatchPreferenceDoesNotMigrateExistingStandardNode(t *testing.T) {
	claim := &karpv1.NodeClaim{}
	claim.Status.ImageID = "/images/2404gen2containerd/versions/202609.01.0"
	nodeClass := &v1beta1.AKSNodeClass{}
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
	nodeClass.Status.Images = []v1beta1.NodeImage{{ID: claim.Status.ImageID}}
	nodeClass.Status.SecurityPatchImages = []v1beta1.NodeImage{{ID: "/images/2404gen2containerd/versions/202609.01.0-2026.09.15"}}
	reason, err := (&CloudProvider{}).isImageVersionDrifted(context.Background(), claim, nodeClass)
	if err != nil || reason != "" {
		t.Fatalf("unexpected migration drift: %s, %v", reason, err)
	}
}
