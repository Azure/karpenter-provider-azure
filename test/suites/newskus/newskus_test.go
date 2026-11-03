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

package newskus_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("NewSKUs", func() {
	for _, tc := range familyTestCases {
		It(fmt.Sprintf("should provision a node for family %s (%s)", tc.CanonicalFamily, tc.RepresentativeSize), func() {
			// Save results for report at end
			DeferCleanup(func() {
				switch CurrentSpecReport().State {
				case types.SpecStatePassed:
					tc.Result = resultPass
				case types.SpecStateSkipped:
					tc.Result = resultSkipped
				default:
					tc.Result = resultFail
				}
			})

			if tc.Reason != "" {
				Skip(tc.Reason)
			}

			// Constrain NodePool to this specific VM size
			nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{
					Key:      corev1.LabelOSStable,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{string(corev1.Linux)},
				},
				{
					Key:      corev1.LabelInstanceTypeStable,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{tc.RepresentativeSize},
				},
				{
					Key:      v1beta1.LabelSKUFamily,
					Operator: corev1.NodeSelectorOpExists,
				},
				{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand},
				},
			}

			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyDeploymentWithTimeout(5*time.Minute, deployment)
			env.ExpectCreatedNodeCount("==", 1)
		})
	}
})
