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
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	. "github.com/onsi/gomega"
)

func TestImageCatalogChanged(t *testing.T) {
	standard := []v1beta1.NodeImage{{
		ID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2404gen2containerd/versions/202606.08.1",
	}}
	securityPatch := []v1beta1.NodeImage{{
		ID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2404gen2containerd/versions/202606.08.1-2026.06.13",
	}}

	tests := []struct {
		name           string
		channel        string
		images         []v1beta1.NodeImage
		catalogChanged bool
	}{
		{name: "standard images remain valid on NodeImage", channel: "NodeImage", images: standard},
		{name: "SecurityPatch images remain valid on SecurityPatch", channel: "SecurityPatch", images: securityPatch},
		{name: "standard images are invalidated on SecurityPatch", channel: "SecurityPatch", images: standard, catalogChanged: true},
		{name: "SecurityPatch images are invalidated on NodeImage", channel: "NodeImage", images: securityPatch, catalogChanged: true},
		{name: "empty status needs no separate invalidation", channel: "SecurityPatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := options.ToContext(t.Context(), &options.Options{NodeOSUpgradeChannel: tt.channel})
			g.Expect(imageCatalogChanged(ctx, tt.images)).To(Equal(tt.catalogChanged))
		})
	}
}

func TestIsSecurityPatchImageID(t *testing.T) {
	g := NewWithT(t)
	g.Expect(isSecurityPatchImageID("/galleries/g/images/i/versions/202606.08.1-2026.06.13")).To(BeTrue())
	g.Expect(isSecurityPatchImageID("/galleries/g/images/i/versions/202606.08.1")).To(BeFalse())
	g.Expect(isSecurityPatchImageID("/galleries/g/images/i/versions/standard-preview")).To(BeFalse())
	g.Expect(isSecurityPatchImageID("not-an-image-id")).To(BeFalse())
}
