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

package fleet

import (
	"regexp"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/onsi/gomega"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

// mkReq builds a FleetVMProvisionRequest with sane defaults and applies functional option mods.
func mkReq(mods ...func(*FleetVMProvisionRequest)) *FleetVMProvisionRequest {
	encrypt := false
	req := &FleetVMProvisionRequest{
		NodeClaimName:       "nc-default",
		CapacityType:        karpv1.CapacityTypeOnDemand,
		AcceptableSKUs:      []string{"Standard_D2s_v3", "Standard_D4s_v3"},
		AcceptableZones:     []string{"1", "2"},
		Tags:                map[string]*string{},
		SSHPublicKey:        "ssh-rsa AAAA...",
		AdminUsername:       "azureuser",
		NodeIdentities:      []string{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/uai1"},
		DiskEncryptionSetID: "",
		NSG:                 "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
		Location:            "eastus",
		NodeClaim: &karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nc-default",
				Labels: map[string]string{
					karpv1.NodePoolLabelKey: "default-pool",
				},
			},
		},
		NodeClass: &v1beta1.AKSNodeClass{
			Spec: v1beta1.AKSNodeClassSpec{
				Security: &v1beta1.Security{
					EncryptionAtHost: &encrypt,
				},
			},
		},
		LaunchTemplate: &launchtemplate.Template{
			ScriptlessCustomData:      "IyEvYmluL2Jhc2gK", // "#!/bin/bash\n"
			ImageID:                   "/subscriptions/sub/.../images/img",
			SubnetID:                  "/subscriptions/sub/.../subnets/subnet1",
			StorageProfileSizeGB:      128,
			StorageProfilePlacement:   armcompute.DiffDiskPlacementCacheDisk,
			StorageProfileIsEphemeral: true,
		},
	}
	for _, m := range mods {
		m(req)
	}
	return req
}

func withTags(t map[string]*string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) { r.Tags = t }
}
func withNodeClaimName(n string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) {
		r.NodeClaimName = n
		r.NodeClaim.Name = n
	}
}
func withCapacityType(c string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) { r.CapacityType = c }
}
func withSKUs(s ...string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) { r.AcceptableSKUs = s }
}
func withZones(z ...string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) { r.AcceptableZones = z }
}
func withIdentities(ids ...string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) { r.NodeIdentities = ids }
}
func withImageID(id string) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) { r.LaunchTemplate.ImageID = id }
}
func withEncryptionAtHost(v bool) func(*FleetVMProvisionRequest) {
	return func(r *FleetVMProvisionRequest) {
		r.NodeClass.Spec.Security.EncryptionAtHost = &v
	}
}

func mustKey(t *testing.T, req *FleetVMProvisionRequest) string {
	t.Helper()
	k, err := DetermineBatchKey(req)
	if err != nil {
		t.Fatalf("DetermineBatchKey failed: %v", err)
	}
	return k
}

// TestDetermineBatchKey_EqualityOnIdenticalFields verifies that two requests built
// from identical inputs produce the same batch key. This is the fundamental
// determinism contract: same input → same key → same batch.
func TestDetermineBatchKey_EqualityOnIdenticalFields(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq())
	b := mustKey(t, mkReq())
	g.Expect(a).To(gomega.Equal(b))
}

// TestDetermineBatchKey_TagsIgnored verifies that Tags are per-VM metadata and
// must not influence the batch key. If they did, every NodeClaim (which carries
// its own nodeclaim-name tag) would land in its own Fleet and batching would be
// a no-op. Covers: tag with different value, tag with extra key, and nil tags.
func TestDetermineBatchKey_TagsIgnored(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	v1 := "x"
	v2 := "y"
	a := mustKey(t, mkReq(withTags(map[string]*string{"k1": &v1})))
	b := mustKey(t, mkReq(withTags(map[string]*string{"k1": &v2, "k2": &v1})))
	c := mustKey(t, mkReq(withTags(nil)))
	g.Expect(a).To(gomega.Equal(b))
	g.Expect(a).To(gomega.Equal(c))
}

// TestDetermineBatchKey_NodeClaimNameIgnored verifies that NodeClaimName is
// per-VM identity, not a grouping signal. Two NodeClaims with otherwise identical
// specs must batch together regardless of name.
func TestDetermineBatchKey_NodeClaimNameIgnored(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withNodeClaimName("nc-abc")))
	b := mustKey(t, mkReq(withNodeClaimName("nc-xyz")))
	g.Expect(a).To(gomega.Equal(b))
}

// TestDetermineBatchKey_CapacityTypeFlipsKey verifies that spot and on-demand
// requests never share a Fleet. The Fleet API requires a single capacity type per
// Fleet (spot uses PriceCapacityOptimized + evictionPolicy=Delete; on-demand uses
// LowestPrice), so mixing the two in one batch would be a body-build failure.
func TestDetermineBatchKey_CapacityTypeFlipsKey(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withCapacityType(karpv1.CapacityTypeSpot)))
	b := mustKey(t, mkReq(withCapacityType(karpv1.CapacityTypeOnDemand)))
	g.Expect(a).ToNot(gomega.Equal(b))
}

// TestDetermineBatchKey_SKUOrderIrrelevant verifies that the order of
// AcceptableSKUs in the request does not affect the key. Two requests with the
// same SKU set in different orders must batch together; otherwise the scheduler's
// non-deterministic ordering of instance types would silently fragment batches.
func TestDetermineBatchKey_SKUOrderIrrelevant(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withSKUs("Standard_D2s_v3", "Standard_D4s_v3")))
	b := mustKey(t, mkReq(withSKUs("Standard_D4s_v3", "Standard_D2s_v3")))
	g.Expect(a).To(gomega.Equal(b))
}

// TestDetermineBatchKey_ZoneOrderIrrelevant verifies the same order-insensitivity
// property as the SKU test, but for AcceptableZones. Zones come from scheduling
// constraints in non-deterministic order.
func TestDetermineBatchKey_ZoneOrderIrrelevant(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withZones("1", "2", "3")))
	b := mustKey(t, mkReq(withZones("3", "1", "2")))
	g.Expect(a).To(gomega.Equal(b))
}

// TestDetermineBatchKey_NodeIdentityOrderIrrelevant verifies that the order of
// user-assigned managed identities does not affect the key. Identity ordering is
// not semantically meaningful in the Fleet body, so two requests with the same
// identity set in different orders must batch together.
func TestDetermineBatchKey_NodeIdentityOrderIrrelevant(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withIdentities("uai-a", "uai-b")))
	b := mustKey(t, mkReq(withIdentities("uai-b", "uai-a")))
	g.Expect(a).To(gomega.Equal(b))
}

// TestDetermineBatchKey_DifferentImageIDFlipsKey verifies that two requests
// resolving to different OS images cannot share a Fleet. The image is embedded
// in baseVirtualMachineProfile.storageProfile.imageReference, which is single-
// valued per Fleet, so requests with different images must batch separately.
func TestDetermineBatchKey_DifferentImageIDFlipsKey(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withImageID("/img/A")))
	b := mustKey(t, mkReq(withImageID("/img/B")))
	g.Expect(a).ToNot(gomega.Equal(b))
}

// TestDetermineBatchKey_DifferentEncryptionAtHostFlipsKey verifies that
// EncryptionAtHost differences flip the key. It also exercises the
// NodeClass.GetEncryptionAtHost() helper's nil-chain handling — if the helper
// regressed and started returning a constant value, this test would catch it.
func TestDetermineBatchKey_DifferentEncryptionAtHostFlipsKey(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	a := mustKey(t, mkReq(withEncryptionAtHost(false)))
	b := mustKey(t, mkReq(withEncryptionAtHost(true)))
	g.Expect(a).ToNot(gomega.Equal(b))
}

// TestDetermineBatchKey_NilRequestReturnsError verifies the nil-safety contract:
// a nil request must produce a non-nil error rather than panic or return an empty
// key. The batcher wraps this error and surfaces it to the caller of Enqueue.
func TestDetermineBatchKey_NilRequestReturnsError(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	_, err := DetermineBatchKey(nil)
	g.Expect(err).To(gomega.HaveOccurred())
}

// TestDetermineBatchKey_KeyFormatPrefix locks in the key's external shape:
// "<nodepool>/<capacityType>/<16 hex chars>". Logs, metrics, and human-readable
// traces parse this prefix, so silently changing the format (e.g. swapping the
// separator or dropping the prefix) would break observability. The regex acts
// as a structural contract.
func TestDetermineBatchKey_KeyFormatPrefix(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	k := mustKey(t, mkReq())
	// Format: "<nodepool>/<capacityType>/<16 hex chars>"
	pattern := `^default-pool/` + karpv1.CapacityTypeOnDemand + `/[0-9a-f]{16}$`
	g.Expect(regexp.MustCompile(pattern).MatchString(k)).To(gomega.BeTrue(), "key %q did not match %s", k, pattern)
}
