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

package customscriptsbootstrap

import (
	"encoding/base64"
	"fmt"
	"math"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestReverseVMMemoryOverhead(t *testing.T) {
	type Cases struct {
		name                    string
		originalGiB             int64
		vmMemoryOverheadPercent float64
	}
	var cases []Cases

	// Generate test cases from originalGiB 1 to 2000 x vmMemoryOverheadPercent 0 to 0.25 using for loops
	// In fact, mathematically, the reverse won't be perfectly equal, but current accuracy in GiB should be enough
	for i := 1; i <= 2000; i++ {
		for j := 0; j <= 250; j++ {
			cases = append(cases, Cases{
				name:                    fmt.Sprintf("Test %d - %f", i, float64(j)/1000),
				originalGiB:             int64(i),
				vmMemoryOverheadPercent: float64(j) / 1000,
			})
		}
	}
	t.Run("2000 x 0.25", func(t *testing.T) {
		for _, tc := range cases {
			subtracted := instancetype.CalculateMemoryWithoutOverhead(tc.vmMemoryOverheadPercent, float64(tc.originalGiB)).Value()
			reversedGiB := int64(math.Round(reverseVMMemoryOverhead(tc.vmMemoryOverheadPercent, float64(subtracted)) / 1024 / 1024 / 1024))
			if tc.originalGiB != reversedGiB {
				t.Errorf("Expected %d but got %d", tc.originalGiB, reversedGiB)
			}
		}
	})
}

func TestConvertContainerLogMaxSizeToMB(t *testing.T) {
	tests := []struct {
		name                string
		containerLogMaxSize string
		expected            *int32
	}{
		{
			name:                "Default",
			containerLogMaxSize: "50Mi",
			expected:            lo.ToPtr(int32(50)),
		},
		{
			name:                "Valid size in Mi",
			containerLogMaxSize: "1024Mi",
			expected:            lo.ToPtr(int32(1024)),
		},
		{
			name:                "Valid size in Gi",
			containerLogMaxSize: "1Gi",
			expected:            lo.ToPtr(int32(1024)),
		},
		{
			name:                "Valid size in Ki",
			containerLogMaxSize: "1048576Ki",
			expected:            lo.ToPtr(int32(1024)),
		},
		{
			name:                "Invalid size",
			containerLogMaxSize: "invalid",
			expected:            nil,
		},
		{
			name:                "Empty size",
			containerLogMaxSize: "",
			expected:            nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertContainerLogMaxSizeToMB(tt.containerLogMaxSize)
			if tt.expected == nil && result != nil {
				t.Errorf("Expected nil but got %v", *result)
			} else if tt.expected != nil && result == nil {
				t.Errorf("Expected %v but got nil", *tt.expected)
			} else if tt.expected != nil && result != nil && *tt.expected != *result {
				t.Errorf("Expected %v but got %v", *tt.expected, *result)
			}
		})
	}
}

func TestConvertPodMaxPids(t *testing.T) {
	tests := []struct {
		name         string
		podPidsLimit *int64
		expected     *int32
	}{
		{
			name:         "Valid PIDs limit within int32 range",
			podPidsLimit: lo.ToPtr(int64(1000)),
			expected:     lo.ToPtr(int32(1000)),
		},
		{
			name:         "PIDs limit exceeding int32 range",
			podPidsLimit: lo.ToPtr(int64(math.MaxInt32) + int64(1)),
			expected:     lo.ToPtr(int32(math.MaxInt32)),
		},
		{
			name:         "PIDs limit at int32 max value",
			podPidsLimit: lo.ToPtr(int64(math.MaxInt32)),
			expected:     lo.ToPtr(int32(math.MaxInt32)),
		},
		{
			name:         "PIDs limit almost at int32 max value",
			podPidsLimit: lo.ToPtr(int64(math.MaxInt32 - 1)),
			expected:     lo.ToPtr(int32(math.MaxInt32 - 1)),
		},
		{
			name:         "Nil PIDs limit",
			podPidsLimit: nil,
			expected:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertPodMaxPids(tt.podPidsLimit)
			if tt.expected == nil && result != nil {
				t.Errorf("Expected nil but got %v", *result)
			} else if tt.expected != nil && result == nil {
				t.Errorf("Expected %v but got nil", *tt.expected)
			} else if tt.expected != nil && result != nil && *tt.expected != *result {
				t.Errorf("Expected %v but got %v", *tt.expected, *result)
			}
		})
	}
}

func TestHydrateBootstrapTokenIfNeeded(t *testing.T) {
	tests := []struct {
		name                   string
		customDataDehydratable string
		cseDehydratable        string
		bootstrapToken         string
		expectedCustomData     string
		expectedCSE            string
		expectError            bool
	}{
		{
			name:                   "Valid token replacement",
			customDataDehydratable: base64.StdEncoding.EncodeToString([]byte("custom-data-with-{{.TokenID}}.{{.TokenSecret}}-placeholder")),
			cseDehydratable:        "cse-with-{{.TokenID}}.{{.TokenSecret}}-placeholder",
			bootstrapToken:         "abc.123456",
			expectedCustomData:     base64.StdEncoding.EncodeToString([]byte("custom-data-with-abc.123456-placeholder")),
			expectedCSE:            "cse-with-abc.123456-placeholder",
			expectError:            false,
		},
		{
			name:                   "No token placeholders",
			customDataDehydratable: base64.StdEncoding.EncodeToString([]byte("custom-data-without-token-placeholder")),
			cseDehydratable:        "cse-without-token-placeholder",
			bootstrapToken:         "abc.123456",
			expectedCustomData:     base64.StdEncoding.EncodeToString([]byte("custom-data-without-token-placeholder")),
			expectedCSE:            "cse-without-token-placeholder",
			expectError:            false,
		},
		{
			name:                   "Invalid base64 encoding",
			customDataDehydratable: "invalid-base64",
			cseDehydratable:        "cse-with-{{.TokenID}}.{{.TokenSecret}}-placeholder",
			bootstrapToken:         "abc.123456",
			expectError:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			customData, cse, err := hydrateBootstrapTokenIfNeeded(tt.customDataDehydratable, tt.cseDehydratable, tt.bootstrapToken)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedCustomData, customData)
			assert.Equal(t, tt.expectedCSE, cse)
		})
	}
}

func TestConvertLocalDNSToModel(t *testing.T) {
	tests := []struct {
		name     string
		localDNS *v1beta1.LocalDNS
		expected *models.LocalDNSProfile
	}{
		{
			name:     "Nil LocalDNS",
			localDNS: nil,
			expected: nil,
		},
		{
			name: "LocalDNS with mode only",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
			},
			expected: &models.LocalDNSProfile{
				Mode: lo.ToPtr("Required"),
			},
		},
		{
			name: "LocalDNS with VnetDNSOverrides",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModePreferred,
				VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:         "example.com",
						QueryLogging: v1beta1.LocalDNSQueryLoggingLog,
						Protocol:     v1beta1.LocalDNSProtocolForceTCP,
					},
				},
			},
			expected: &models.LocalDNSProfile{
				Mode: lo.ToPtr("Preferred"),
				VnetDNSOverrides: models.LocalDNSOverrides{
					"example.com": models.LocalDNSOverride{
						QueryLogging: lo.ToPtr("Log"),
						Protocol:     lo.ToPtr("ForceTCP"),
					},
				},
			},
		},
		{
			name: "LocalDNS with KubeDNSOverrides",
			localDNS: &v1beta1.LocalDNS{
				KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:               "cluster.local",
						ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
						MaxConcurrent:      lo.ToPtr(int32(100)),
					},
				},
			},
			expected: &models.LocalDNSProfile{
				KubeDNSOverrides: models.LocalDNSOverrides{
					"cluster.local": models.LocalDNSOverride{
						ForwardDestination: lo.ToPtr("ClusterCoreDNS"),
						MaxConcurrent:      lo.ToPtr(int32(100)),
					},
				},
			},
		},
		{
			name: "LocalDNS with both VnetDNS and KubeDNS overrides",
			localDNS: &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeDisabled,
				VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:     "vnet.domain",
						Protocol: v1beta1.LocalDNSProtocolPreferUDP,
					},
				},
				KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					{
						Zone:          "kube.domain",
						ForwardPolicy: v1beta1.LocalDNSForwardPolicyRoundRobin,
					},
				},
			},
			expected: &models.LocalDNSProfile{
				Mode: lo.ToPtr("Disabled"),
				VnetDNSOverrides: models.LocalDNSOverrides{
					"vnet.domain": models.LocalDNSOverride{
						Protocol: lo.ToPtr("PreferUDP"),
					},
				},
				KubeDNSOverrides: models.LocalDNSOverrides{
					"kube.domain": models.LocalDNSOverride{
						ForwardPolicy: lo.ToPtr("RoundRobin"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertLocalDNSToModel(tt.localDNS)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertLocalDNSZoneOverrideToModel(t *testing.T) {
	tests := []struct {
		name     string
		override *v1beta1.LocalDNSZoneOverride
		expected *models.LocalDNSOverride
	}{
		{
			name:     "Nil override",
			override: nil,
			expected: nil,
		},
		{
			name: "Override with all fields",
			override: &v1beta1.LocalDNSZoneOverride{
				Zone:               "example.com",
				QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
				Protocol:           v1beta1.LocalDNSProtocolForceTCP,
				ForwardDestination: v1beta1.LocalDNSForwardDestinationVnetDNS,
				ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
				MaxConcurrent:      lo.ToPtr(int32(50)),
				CacheDuration:      karpv1.MustParseNillableDuration("1h"),
				ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
				ServeStale:         v1beta1.LocalDNSServeStaleVerify,
			},
			expected: &models.LocalDNSOverride{
				QueryLogging:                lo.ToPtr("Error"),
				Protocol:                    lo.ToPtr("ForceTCP"),
				ForwardDestination:          lo.ToPtr("VnetDNS"),
				ForwardPolicy:               lo.ToPtr("Sequential"),
				MaxConcurrent:               lo.ToPtr(int32(50)),
				CacheDurationInSeconds:      lo.ToPtr(int32(3600)), // 1 hour = 3600 seconds
				ServeStaleDurationInSeconds: lo.ToPtr(int32(1800)), // 30 minutes = 1800 seconds
				ServeStale:                  lo.ToPtr("Verify"),
			},
		},
		{
			name: "Override with only duration fields",
			override: &v1beta1.LocalDNSZoneOverride{
				Zone:               "example.com",
				CacheDuration:      karpv1.MustParseNillableDuration("2h30m"),
				ServeStaleDuration: karpv1.MustParseNillableDuration("45m"),
			},
			expected: &models.LocalDNSOverride{
				CacheDurationInSeconds:      lo.ToPtr(int32(9000)), // 2.5 hours = 9000 seconds
				ServeStaleDurationInSeconds: lo.ToPtr(int32(2700)), // 45 minutes = 2700 seconds
			},
		},
		{
			name: "Override with complex duration",
			override: &v1beta1.LocalDNSZoneOverride{
				Zone:               "example.com",
				CacheDuration:      karpv1.MustParseNillableDuration("1h30m45s"),
				ServeStaleDuration: karpv1.MustParseNillableDuration("15m30s"),
			},
			expected: &models.LocalDNSOverride{
				CacheDurationInSeconds:      lo.ToPtr(int32(5445)), // 1h30m45s = 5445 seconds
				ServeStaleDurationInSeconds: lo.ToPtr(int32(930)),  // 15m30s = 930 seconds
			},
		},
		{
			name: "Override with zero duration values",
			override: &v1beta1.LocalDNSZoneOverride{
				Zone:               "example.com",
				CacheDuration:      karpv1.NillableDuration{},
				ServeStaleDuration: karpv1.NillableDuration{},
			},
			expected: &models.LocalDNSOverride{},
		},
		{
			name: "Override with only enum fields",
			override: &v1beta1.LocalDNSZoneOverride{
				Zone:               "example.com",
				QueryLogging:       v1beta1.LocalDNSQueryLoggingLog,
				Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
				ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
				ForwardPolicy:      v1beta1.LocalDNSForwardPolicyRandom,
				ServeStale:         v1beta1.LocalDNSServeStaleImmediate,
			},
			expected: &models.LocalDNSOverride{
				QueryLogging:       lo.ToPtr("Log"),
				Protocol:           lo.ToPtr("PreferUDP"),
				ForwardDestination: lo.ToPtr("ClusterCoreDNS"),
				ForwardPolicy:      lo.ToPtr("Random"),
				ServeStale:         lo.ToPtr("Immediate"),
			},
		},
		{
			name: "Override with MaxConcurrent only",
			override: &v1beta1.LocalDNSZoneOverride{
				Zone:          "example.com",
				MaxConcurrent: lo.ToPtr(int32(200)),
			},
			expected: &models.LocalDNSOverride{
				MaxConcurrent: lo.ToPtr(int32(200)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertLocalDNSZoneOverrideToModel(tt.override)
			assert.Equal(t, tt.expected, result)
		})
	}
}
