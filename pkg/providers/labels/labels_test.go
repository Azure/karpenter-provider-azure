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

package labels

import (
	"context"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLocalDNSLabels(t *testing.T) {
	testCases := []struct {
		name                   string
		localDNS               *v1beta1.LocalDNS
		kubernetesVersion      string
		expectedLabel          string
		expectedLabelShouldSet bool
	}{
		{
			name: "LocalDNS mode is Required",
			localDNS: &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
			},
			kubernetesVersion:      "1.35.0",
			expectedLabel:          "enabled",
			expectedLabelShouldSet: true,
		},
		{
			name: "LocalDNS mode is Disabled",
			localDNS: &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeDisabled),
			},
			kubernetesVersion:      "1.35.0",
			expectedLabel:          "disabled",
			expectedLabelShouldSet: true,
		},
		{
			name: "LocalDNS mode is Preferred with k8s >= 1.36",
			localDNS: &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModePreferred),
			},
			kubernetesVersion:      "1.36.0",
			expectedLabel:          "enabled",
			expectedLabelShouldSet: true,
		},
		{
			name: "LocalDNS mode is Preferred with k8s 1.37",
			localDNS: &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModePreferred),
			},
			kubernetesVersion:      "1.37.0",
			expectedLabel:          "enabled",
			expectedLabelShouldSet: true,
		},
		{
			name: "LocalDNS mode is Preferred with k8s < 1.36",
			localDNS: &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModePreferred),
			},
			kubernetesVersion:      "1.35.0",
			expectedLabel:          "",
			expectedLabelShouldSet: false,
		},
		{
			name: "LocalDNS mode is Preferred with k8s 1.35.9",
			localDNS: &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModePreferred),
			},
			kubernetesVersion:      "1.35.9",
			expectedLabel:          "",
			expectedLabelShouldSet: false,
		},
		{
			name:                   "LocalDNS is nil",
			localDNS:               nil,
			kubernetesVersion:      "1.36.0",
			expectedLabel:          "",
			expectedLabelShouldSet: false,
		},
		{
			name: "LocalDNS mode is nil",
			localDNS: &v1beta1.LocalDNS{
				Mode: nil,
			},
			kubernetesVersion:      "1.36.0",
			expectedLabel:          "",
			expectedLabelShouldSet: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := options.ToContext(context.Background(), &options.Options{
				NodeResourceGroup:       "test-rg",
				KubeletIdentityClientID: "test-client-id",
				SubnetID:                "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/test/subnets/test",
			})

			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodeclass",
				},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: tc.localDNS,
				},
				Status: v1beta1.AKSNodeClassStatus{
					KubernetesVersion: tc.kubernetesVersion,
					Conditions: []status.Condition{
						{
							Type:   v1beta1.ConditionTypeKubernetesVersionReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			labels, err := Get(ctx, nodeClass)
			assert.NoError(t, err)

			if tc.expectedLabelShouldSet {
				assert.Equal(t, tc.expectedLabel, labels[AKSLocalDNSStateLabelKey], "Expected localdns-state label to be set")
			} else {
				assert.NotContains(t, labels, AKSLocalDNSStateLabelKey, "Expected localdns-state label to not be set")
			}
		})
	}
}
