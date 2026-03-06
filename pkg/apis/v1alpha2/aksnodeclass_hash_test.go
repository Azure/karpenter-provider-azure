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

package v1alpha2_test

import (
	"testing"
	"time"

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

func newHashTestNodeClassV1Alpha2() *v1alpha2.AKSNodeClass {
	return &v1alpha2.AKSNodeClass{
		ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
		Spec: v1alpha2.AKSNodeClassSpec{
			VNETSubnetID: lo.ToPtr("subnet-id"),
			OSDiskSizeGB: lo.ToPtr(int32(30)),
			ImageFamily:  lo.ToPtr("Ubuntu2204"),
			Tags: map[string]string{
				"keyTag-1": "valueTag-1",
				"keyTag-2": "valueTag-2",
			},
			Kubelet: &v1alpha2.KubeletConfiguration{
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
	// NOTE: When the hashing algorithm is updated, these tests are expected to fail; test hash constants here would have to be updated, and currentHashVersion would have to be updated to the new version matching v1alpha2.AKSNodeClassHashVersion
	const staticHash = "4108492229247269128"

	t.Run("should match static hash on field value change", func(t *testing.T) {
		tests := []struct {
			name    string
			hash    string
			changes v1alpha2.AKSNodeClass
		}{
			{"Base AKSNodeClass", staticHash, v1alpha2.AKSNodeClass{}},
			// Static fields, expect changed hash from base
			{"VNETSubnetID", "13971920214979852468", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}},
			{"OSDiskSizeGB", "7816855636861645563", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}},
			{"ImageFamily", "15616969746300892810", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}},
			{"Kubelet", "33638514539106194", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{Kubelet: &v1alpha2.KubeletConfiguration{CPUManagerPolicy: lo.ToPtr("none")}}}},
			{"MaxPods", "15508761509963240710", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}},
			{"LocalDNS.Mode", "17805442572569734619", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{Mode: v1alpha2.LocalDNSModeRequired}}}},
			{"LocalDNS.VnetDNSOverrides", "14608914734386108436", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", QueryLogging: v1alpha2.LocalDNSQueryLoggingLog}}}}}},
			{"LocalDNS.KubeDNSOverrides", "4529827108104295737", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{KubeDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1alpha2.LocalDNSProtocolForceTCP}}}}}},
			{"LocalDNS.VnetDNSOverrides.CacheDuration", "11008649797056761238", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", CacheDuration: karpv1.MustParseNillableDuration("1h")}}}}}},
			{"LocalDNS.VnetDNSOverrides.ServeStaleDuration", "4895720480850206885", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", ServeStaleDuration: karpv1.MustParseNillableDuration("30m")}}}}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				nodeClass := newHashTestNodeClassV1Alpha2()
				if err := mergo.Merge(nodeClass, tt.changes, mergo.WithOverride, mergo.WithSliceDeepCopy); err != nil {
					t.Fatalf("mergo.Merge failed: %v", err)
				}
				if got := nodeClass.Hash(); got != tt.hash {
					t.Errorf("Hash() = %s, want %s", got, tt.hash)
				}
			})
		}
	})

	t.Run("should match static hash when reordering tags", func(t *testing.T) {
		nodeClass := newHashTestNodeClassV1Alpha2()
		nodeClass.Spec.Tags = map[string]string{"keyTag-2": "valueTag-2", "keyTag-1": "valueTag-1"}
		if got := nodeClass.Hash(); got != staticHash {
			t.Errorf("Hash() = %s, want %s after tag reorder", got, staticHash)
		}
	})

	t.Run("should change hash when static fields are updated", func(t *testing.T) {
		tests := []struct {
			name    string
			changes v1alpha2.AKSNodeClass
		}{
			{"VNETSubnetID", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}},
			{"OSDiskSizeGB", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}},
			{"ImageFamily", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}},
			{"Kubelet", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{Kubelet: &v1alpha2.KubeletConfiguration{CPUManagerPolicy: lo.ToPtr("none")}}}},
			{"MaxPods", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}},
			{"LocalDNS.Mode", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{Mode: v1alpha2.LocalDNSModeRequired}}}},
			{"LocalDNS.VnetDNSOverrides", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", QueryLogging: v1alpha2.LocalDNSQueryLoggingLog}}}}}},
			{"LocalDNS.KubeDNSOverrides", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{KubeDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1alpha2.LocalDNSProtocolForceTCP}}}}}},
			{"LocalDNS.VnetDNSOverrides.CacheDuration", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", CacheDuration: karpv1.MustParseNillableDuration("2h")}}}}}},
			{"LocalDNS.VnetDNSOverrides.ServeStaleDuration", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: []v1alpha2.LocalDNSZoneOverride{{Zone: "example.com", ServeStaleDuration: karpv1.MustParseNillableDuration("1h")}}}}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				nodeClass := newHashTestNodeClassV1Alpha2()
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

	t.Run("should not change hash when tags are changed", func(t *testing.T) {
		nodeClass := newHashTestNodeClassV1Alpha2()
		hash := nodeClass.Hash()
		nodeClass.Spec.Tags = map[string]string{"keyTag-3": "valueTag-3"}
		updatedHash := nodeClass.Hash()
		if hash != updatedHash {
			t.Errorf("expected hash to remain %s after tag change, got %s", hash, updatedHash)
		}
	})

	t.Run("should expect two AKSNodeClasses with the same spec to have the same hash", func(t *testing.T) {
		nodeClass := newHashTestNodeClassV1Alpha2()
		otherNodeClass := &v1alpha2.AKSNodeClass{
			Spec: nodeClass.Spec,
		}
		if nodeClass.Hash() != otherNodeClass.Hash() {
			t.Errorf("expected same hash for identical specs, got %s and %s", nodeClass.Hash(), otherNodeClass.Hash())
		}
	})

	// This test is a sanity check to update the hashing version if the algorithm has been updated.
	// Note: this will only catch a missing version update, if the staticHash hasn't been updated yet.
	t.Run("when hashing algorithm updates, we should update the hash version", func(t *testing.T) {
		nodeClass := newHashTestNodeClassV1Alpha2()
		currentHashVersion := "v3"
		if nodeClass.Hash() != staticHash {
			if v1alpha2.AKSNodeClassHashVersion == currentHashVersion {
				t.Errorf("hash changed from static hash, expected AKSNodeClassHashVersion to differ from %s", currentHashVersion)
			}
		} else {
			// Note: this failure case is to ensure you have updated currentHashVersion, not AKSNodeClassHashVersion
			if currentHashVersion != v1alpha2.AKSNodeClassHashVersion {
				t.Errorf("expected currentHashVersion %s to equal AKSNodeClassHashVersion %s", currentHashVersion, v1alpha2.AKSNodeClassHashVersion)
			}
		}
		// Note: this failure case is to ensure you have updated staticHash value
		if staticHash != nodeClass.Hash() {
			t.Errorf("expected staticHash %s to equal computed hash %s", staticHash, nodeClass.Hash())
		}
	})
}
