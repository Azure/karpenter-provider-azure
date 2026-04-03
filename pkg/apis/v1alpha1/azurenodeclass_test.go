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

package v1alpha1

import (
	"testing"

	"github.com/samber/lo"
)

func TestAzureNodeClassHash(t *testing.T) {
	nc := &AzureNodeClass{
		Spec: AzureNodeClassSpec{
			ImageID:      lo.ToPtr("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/galleries/testgallery/images/testimage/versions/1.0.0"),
			OSDiskSizeGB: lo.ToPtr[int32](128),
		},
	}

	// Hash should return a stable value for the same spec
	hash1 := nc.Hash()
	hash2 := nc.Hash()
	if hash1 != hash2 {
		t.Errorf("Hash() returned different values for the same spec: %q vs %q", hash1, hash2)
	}
	if hash1 == "" {
		t.Error("Hash() returned empty string")
	}

	// Hash should change when spec changes
	nc.Spec.OSDiskSizeGB = lo.ToPtr[int32](256)
	hash3 := nc.Hash()
	if hash3 == hash1 {
		t.Errorf("Hash() returned same value after spec change: %q", hash3)
	}

	// Hash should change when ImageID changes
	nc.Spec.ImageID = lo.ToPtr("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/galleries/othergallery/images/otherimage/versions/2.0.0")
	hash4 := nc.Hash()
	if hash4 == hash3 {
		t.Errorf("Hash() returned same value after ImageID change: %q", hash4)
	}

	// Tags are ignored in hash (hash:"ignore" tag on Tags field)
	hash5 := nc.Hash()
	nc.Spec.Tags = map[string]string{"env": "test"}
	hash6 := nc.Hash()
	if hash5 != hash6 {
		t.Errorf("Hash() should ignore Tags, but got different hashes: %q vs %q", hash5, hash6)
	}
}

func TestAzureNodeClassGetters(t *testing.T) {
	imageID := "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Compute/galleries/testgallery/images/testimage/versions/1.0.0"
	userData := "#!/bin/bash\necho hello"
	vnetSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
	osDiskSizeGB := int32(256)
	tags := map[string]string{"env": "test", "team": "platform"}

	nc := &AzureNodeClass{
		Spec: AzureNodeClassSpec{
			ImageID:      lo.ToPtr(imageID),
			UserData:     lo.ToPtr(userData),
			VNETSubnetID: lo.ToPtr(vnetSubnetID),
			OSDiskSizeGB: lo.ToPtr(osDiskSizeGB),
			Tags:         tags,
			Security: &AzureNodeClassSecurity{
				EncryptionAtHost: lo.ToPtr(true),
			},
		},
	}

	// Test GetImageID
	if got := nc.GetImageID(); got == nil || *got != imageID {
		t.Errorf("GetImageID() = %v, want %q", got, imageID)
	}

	// Test GetUserData
	if got := nc.GetUserData(); got == nil || *got != userData {
		t.Errorf("GetUserData() = %v, want %q", got, userData)
	}

	// Test GetVNETSubnetID
	if got := nc.GetVNETSubnetID(); got == nil || *got != vnetSubnetID {
		t.Errorf("GetVNETSubnetID() = %v, want %q", got, vnetSubnetID)
	}

	// Test GetOSDiskSizeGB
	if got := nc.GetOSDiskSizeGB(); got == nil || *got != osDiskSizeGB {
		t.Errorf("GetOSDiskSizeGB() = %v, want %d", got, osDiskSizeGB)
	}

	// Test GetTags
	gotTags := nc.GetTags()
	if len(gotTags) != len(tags) {
		t.Errorf("GetTags() returned %d tags, want %d", len(gotTags), len(tags))
	}
	for k, v := range tags {
		if gotTags[k] != v {
			t.Errorf("GetTags()[%q] = %q, want %q", k, gotTags[k], v)
		}
	}

	// Test GetEncryptionAtHost
	if got := nc.GetEncryptionAtHost(); got != true {
		t.Errorf("GetEncryptionAtHost() = %v, want true", got)
	}

	// Test GetEncryptionAtHost with nil Security
	ncNoSecurity := &AzureNodeClass{Spec: AzureNodeClassSpec{ImageID: lo.ToPtr(imageID)}}
	if got := ncNoSecurity.GetEncryptionAtHost(); got != false {
		t.Errorf("GetEncryptionAtHost() with nil Security = %v, want false", got)
	}

	// Test GetEncryptionAtHost with nil EncryptionAtHost
	ncNilEncryption := &AzureNodeClass{
		Spec: AzureNodeClassSpec{
			ImageID:  lo.ToPtr(imageID),
			Security: &AzureNodeClassSecurity{},
		},
	}
	if got := ncNilEncryption.GetEncryptionAtHost(); got != false {
		t.Errorf("GetEncryptionAtHost() with nil EncryptionAtHost = %v, want false", got)
	}

	// Test getters with nil values
	ncNil := &AzureNodeClass{Spec: AzureNodeClassSpec{}}
	if got := ncNil.GetImageID(); got != nil {
		t.Errorf("GetImageID() with nil ImageID = %v, want nil", got)
	}
	if got := ncNil.GetUserData(); got != nil {
		t.Errorf("GetUserData() with nil UserData = %v, want nil", got)
	}
	if got := ncNil.GetVNETSubnetID(); got != nil {
		t.Errorf("GetVNETSubnetID() with nil VNETSubnetID = %v, want nil", got)
	}
	if got := ncNil.GetOSDiskSizeGB(); got != nil {
		t.Errorf("GetOSDiskSizeGB() with nil OSDiskSizeGB = %v, want nil", got)
	}
	if got := ncNil.GetTags(); got != nil {
		t.Errorf("GetTags() with nil Tags = %v, want nil", got)
	}
}

func TestAzureNodeClassStatusConditions(t *testing.T) {
	nc := &AzureNodeClass{}

	condSet := nc.StatusConditions()

	// Verify that ValidationSucceeded is a tracked condition by setting it and checking
	condSet.SetTrue(ConditionTypeValidationSucceeded)

	cond := condSet.Get(ConditionTypeValidationSucceeded)
	if cond.IsUnknown() {
		t.Error("after SetTrue, ValidationSucceeded should not be Unknown")
	}
	if !cond.IsTrue() {
		t.Error("after SetTrue, ValidationSucceeded should be True")
	}

	// Verify that Ready condition exists and can be set
	condSet.SetTrue("Ready")
	readyCond := condSet.Get("Ready")
	if !readyCond.IsTrue() {
		t.Error("after SetTrue, Ready should be True")
	}

	// Verify GetConditions returns the set conditions
	conditions := nc.GetConditions()
	if len(conditions) == 0 {
		t.Error("GetConditions() returned empty slice after setting conditions")
	}

	// Verify SetConditions works
	nc.SetConditions(nil)
	if conditions := nc.GetConditions(); conditions != nil {
		t.Errorf("after SetConditions(nil), GetConditions() = %v, want nil", conditions)
	}
}
