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

package integration_test

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalDNS", func() {
	Context("LocalDNS Mode", func() {
		It("should provision a node with LocalDNS mode set to Preferred", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModePreferred),
			}

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify the node was created successfully with LocalDNS configuration
			node := env.Monitor.CreatedNodes()[0]
			Expect(node).ToNot(BeNil())
			Expect(node.Name).ToNot(BeEmpty())
		})

		It("should provision a node with LocalDNS mode set to Required", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
			}

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify the node was created successfully with LocalDNS configuration
			node := env.Monitor.CreatedNodes()[0]
			Expect(node).ToNot(BeNil())
			Expect(node.Name).ToNot(BeEmpty())
		})

		It("should provision a node with LocalDNS mode set to Disabled", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeDisabled),
			}

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify the node was created successfully with LocalDNS configuration
			node := env.Monitor.CreatedNodes()[0]
			Expect(node).ToNot(BeNil())
			Expect(node.Name).ToNot(BeEmpty())
		})
	})

	Context("LocalDNS with Overrides", func() {
		It("should provision a node with comprehensive DNS overrides", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
						ForwardPolicy:      lo.ToPtr(v1beta1.LocalDNSForwardPolicySequential),
						MaxConcurrent:      lo.ToPtr(int32(1000)),
						Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolPreferUDP),
						QueryLogging:       lo.ToPtr(v1beta1.LocalDNSQueryLoggingError),
						ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleVerify),
						ServeStaleDuration: karpv1.MustParseNillableDuration("1h"),
					},
					"cluster.local": {
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
						ForwardPolicy:      lo.ToPtr(v1beta1.LocalDNSForwardPolicySequential),
						MaxConcurrent:      lo.ToPtr(int32(1000)),
						Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolForceTCP),
						QueryLogging:       lo.ToPtr(v1beta1.LocalDNSQueryLoggingError),
						ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleImmediate),
						ServeStaleDuration: karpv1.MustParseNillableDuration("1h"),
					},
				},
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
						ForwardPolicy:      lo.ToPtr(v1beta1.LocalDNSForwardPolicySequential),
						MaxConcurrent:      lo.ToPtr(int32(1000)),
						Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolPreferUDP),
						QueryLogging:       lo.ToPtr(v1beta1.LocalDNSQueryLoggingError),
						ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleVerify),
						ServeStaleDuration: karpv1.MustParseNillableDuration("1h"),
					},
					"cluster.local": {
						CacheDuration:      karpv1.MustParseNillableDuration("1h"),
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
						ForwardPolicy:      lo.ToPtr(v1beta1.LocalDNSForwardPolicySequential),
						MaxConcurrent:      lo.ToPtr(int32(1000)),
						Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolForceTCP),
						QueryLogging:       lo.ToPtr(v1beta1.LocalDNSQueryLoggingError),
						ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleImmediate),
						ServeStaleDuration: karpv1.MustParseNillableDuration("1h"),
					},
				},
			}

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify the node was created successfully with both override types
			node := env.Monitor.CreatedNodes()[0]
			Expect(node).ToNot(BeNil())
			Expect(node.Name).ToNot(BeEmpty())
		})
	})

	Context("LocalDNS Nil Configuration", func() {
		It("should provision a node when LocalDNS is not specified (nil)", func() {
			// Don't set nodeClass.Spec.LocalDNS - test with nil/default behavior
			nodeClass.Spec.LocalDNS = nil

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)

			// Verify the node was created successfully without LocalDNS configuration
			node := env.Monitor.CreatedNodes()[0]
			Expect(node).ToNot(BeNil())
			Expect(node.Name).ToNot(BeEmpty())
		})
	})
})
