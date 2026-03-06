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

package v1beta1_test

import (
	"testing"
	"time"

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

func newHashTestNodeClass() *v1beta1.AKSNodeClass {
	return &v1beta1.AKSNodeClass{
		ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
		Spec: v1beta1.AKSNodeClassSpec{
			VNETSubnetID: lo.ToPtr("subnet-id"),
			OSDiskSizeGB: lo.ToPtr(int32(30)),
			ImageFamily:  lo.ToPtr("Ubuntu2204"),
			Tags: map[string]string{
				"keyTag-1": "valueTag-1",
				"keyTag-2": "valueTag-2",
			},
			Kubelet: &v1beta1.KubeletConfiguration{
				CPUManagerPolicy:            lo.ToPtr("static"),
				CPUCFSQuota:                 lo.ToPtr(true),
				CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
				ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
				ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
				TopologyManagerPolicy:       lo.ToPtr("none"),
				AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
				ContainerLogMaxSize:         lo.ToPtr("10Mi"),
				ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
			},
			MaxPods: lo.ToPtr(int32(100)),
		},
	}
}

//nolint:gocyclo
func TestHash(t *testing.T) {
	// NOTE: When the hashing algorithm is updated, these tests are expected to fail; test hash constants here would have to be updated, and currentHashVersion would have to be updated to the new version matching v1beta1.AKSNodeClassHashVersion
	const staticHash = "4108492229247269128"

	t.Run("should match static hash on field value change", func(t *testing.T) {
		tests := []struct {
			name    string
			hash    string
			changes v1beta1.AKSNodeClass
		}{
			{"Base AKSNodeClass", staticHash, v1beta1.AKSNodeClass{}},
			// Static fields, expect changed hash from base
			{"VNETSubnetID", "13971920214979852468", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}},
			{"OSDiskSizeGB", "7816855636861645563", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}},
			{"ImageFamily", "15616969746300892810", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}},
			{"Kubelet", "33638514539106194", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{Kubelet: &v1beta1.KubeletConfiguration{CPUManagerPolicy: lo.ToPtr("none")}}}},
			{"MaxPods", "15508761509963240710", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}},
			{"LocalDNS.Mode", "17805442572569734619", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}}}},
			{"LocalDNS.VnetDNSOverrides", "14608914734386108436", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", QueryLogging: v1beta1.LocalDNSQueryLoggingLog}}}}}},
			{"LocalDNS.KubeDNSOverrides", "4529827108104295737", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1beta1.LocalDNSProtocolForceTCP}}}}}},
			{"LocalDNS.VnetDNSOverrides.CacheDuration", "11008649797056761238", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", CacheDuration: karpv1.MustParseNillableDuration("1h")}}}}}},
			{"LocalDNS.VnetDNSOverrides.ServeStaleDuration", "4895720480850206885", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", ServeStaleDuration: karpv1.MustParseNillableDuration("30m")}}}}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				nodeClass := newHashTestNodeClass()
				if err := mergo.Merge(nodeClass, tt.changes, mergo.WithOverride, mergo.WithSliceDeepCopy); err != nil {
					t.Fatalf("mergo.Merge failed: %v", err)
				}
				if got := nodeClass.Hash(); got != tt.hash {
					t.Errorf("Hash() = %s, want %s", got, tt.hash)
				}
			})
		}
	})

	t.Run("should change hash when static fields are updated", func(t *testing.T) {
		tests := []struct {
			name    string
			changes v1beta1.AKSNodeClass
		}{
			{"VNETSubnetID", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}},
			{"OSDiskSizeGB", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}},
			{"ImageFamily", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}},
			{"Kubelet", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{Kubelet: &v1beta1.KubeletConfiguration{CPUManagerPolicy: lo.ToPtr("none")}}}},
			{"MaxPods", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}},
			{"LocalDNS.Mode", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}}}},
			{"LocalDNS.VnetDNSOverrides", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", QueryLogging: v1beta1.LocalDNSQueryLoggingLog}}}}}},
			{"LocalDNS.KubeDNSOverrides", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1beta1.LocalDNSProtocolForceTCP}}}}}},
			{"LocalDNS.VnetDNSOverrides.CacheDuration", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", CacheDuration: karpv1.MustParseNillableDuration("2h")}}}}}},
			{"LocalDNS.VnetDNSOverrides.ServeStaleDuration", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", ServeStaleDuration: karpv1.MustParseNillableDuration("1h")}}}}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				nodeClass := newHashTestNodeClass()
				hash := nodeClass.Hash()
				if err := mergo.Merge(nodeClass, tt.changes, mergo.WithOverride, mergo.WithSliceDeepCopy); err != nil {
					t.Fatalf("mergo.Merge failed: %v", err)
				}
				updatedHash := nodeClass.Hash()
				if hash == updatedHash {
					t.Errorf("expected hash to change after updating %s, but got same hash %s", tt.name, hash)
				}
			})
		}
	})

	t.Run("should not change hash when tags are re-ordered", func(t *testing.T) {
		nodeClass := newHashTestNodeClass()
		hash := nodeClass.Hash()
		nodeClass.Spec.Tags = map[string]string{"keyTag-2": "valueTag-2", "keyTag-1": "valueTag-1"}
		updatedHash := nodeClass.Hash()
		if hash != updatedHash {
			t.Errorf("expected hash to remain %s after tag reorder, got %s", hash, updatedHash)
		}
	})

	t.Run("should not change hash when tags are changed", func(t *testing.T) {
		nodeClass := newHashTestNodeClass()
		hash := nodeClass.Hash()
		nodeClass.Spec.Tags = map[string]string{"keyTag-3": "valueTag-3"}
		updatedHash := nodeClass.Hash()
		if hash != updatedHash {
			t.Errorf("expected hash to remain %s after tag change, got %s", hash, updatedHash)
		}
	})

	t.Run("should expect two AKSNodeClasses with the same spec to have the same hash", func(t *testing.T) {
		nodeClass := newHashTestNodeClass()
		otherNodeClass := &v1beta1.AKSNodeClass{
			Spec: nodeClass.Spec,
		}
		if nodeClass.Hash() != otherNodeClass.Hash() {
			t.Errorf("expected same hash for identical specs, got %s and %s", nodeClass.Hash(), otherNodeClass.Hash())
		}
	})

	// This test is a sanity check to update the hashing version if the algorithm has been updated.
	// Note: this will only catch a missing version update, if the staticHash hasn't been updated yet.
	t.Run("when hashing algorithm updates, we should update the hash version", func(t *testing.T) {
		nodeClass := newHashTestNodeClass()
		currentHashVersion := "v3"
		if nodeClass.Hash() != staticHash {
			if v1beta1.AKSNodeClassHashVersion == currentHashVersion {
				t.Errorf("hash changed from static hash, expected AKSNodeClassHashVersion to differ from %s", currentHashVersion)
			}
		} else {
			// Note: this failure case is to ensure you have updated currentHashVersion, not AKSNodeClassHashVersion
			if currentHashVersion != v1beta1.AKSNodeClassHashVersion {
				t.Errorf("expected currentHashVersion %s to equal AKSNodeClassHashVersion %s", currentHashVersion, v1beta1.AKSNodeClassHashVersion)
			}
		}
		// Note: this failure case is to ensure you have updated staticHash value
		if staticHash != nodeClass.Hash() {
			t.Errorf("expected staticHash %s to equal computed hash %s", staticHash, nodeClass.Hash())
		}
	})
}
