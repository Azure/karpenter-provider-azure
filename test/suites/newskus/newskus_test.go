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
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
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

			// NOTE: env.ProvisionMode is currently only set in selfhosted
			// (which is fine in this case because we're also checking for InClusterController)
			if env.ProvisionMode == consts.ProvisionModeAKSScriptless && env.InClusterController {
				// TODO: Remove this when CIG images support NVMe
				Skip("New sizes mostly require NVMe, which requires USE_SIG=true, which we cannot set in the in-cluster controller")
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
			}

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthyWithTimeout(5*time.Minute, pod)
			env.ExpectCreatedNodeCount("==", 1)
		})
	}
})
