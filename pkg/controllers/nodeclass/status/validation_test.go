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

package status_test

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// createZoneOverride creates a LocalDNSZoneOverride with all required fields
func createZoneOverride(zone string, forwardToVnetDNS bool) v1beta1.LocalDNSZoneOverride {
	forwardDest := v1beta1.LocalDNSForwardDestinationClusterCoreDNS
	if forwardToVnetDNS {
		forwardDest = v1beta1.LocalDNSForwardDestinationVnetDNS
	}
	return v1beta1.LocalDNSZoneOverride{
		Zone:               zone,
		QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
		Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
		ForwardDestination: forwardDest,
		ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
		MaxConcurrent:      lo.ToPtr(int32(100)),
		CacheDuration:      karpv1.MustParseNillableDuration("1h"),
		ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
		ServeStale:         v1beta1.LocalDNSServeStaleVerify,
	}
}

var _ = Describe("Validation Reconciler", func() {
	var ctx context.Context
	var reconciler *status.ValidationReconciler
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		ctx = context.Background()

		// Create minimal test setup with fake DES client
		azClient := instance.NewAZClientFromAPI(
			nil,                           // virtualMachinesClient
			nil,                           // azureResourceGraphClient
			nil,                           // aksMachinesClient
			nil,                           // agentPoolsClient
			nil,                           // virtualMachinesExtensionClient
			nil,                           // interfacesClient
			nil,                           // subnetsClient
			&fake.DiskEncryptionSetsAPI{}, // diskEncryptionSetsClient
			nil,                           // loadBalancersClient
			nil,                           // networkSecurityGroupsClient
			nil,                           // imageVersionsClient
			nil,                           // nodeImageVersionsClient
			nil,                           // nodeBootstrappingClient
			nil,                           // skuClient
			nil,                           // subscriptionsClient
		)
		opts := &options.Options{}

		// Note: client is nil since these basic tests don't need to interact with k8s objects
		reconciler = status.NewValidationReconciler(nil, azClient.DiskEncryptionSetsClient(), opts)
		nodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-nodeclass",
				Generation: 1,
			},
			Spec: v1beta1.AKSNodeClassSpec{},
		}
	})

	// All LocalDNS validations are now handled declaratively by CEL and kubebuilder markers.
	// The ValidationReconciler is a skeleton for future runtime validations that cannot be
	// expressed in the CRD schema (e.g., external API calls, cross-resource checks, etc.).

	Context("basic validation reconciliation", func() {
		It("should always set ValidationSucceeded condition to true", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})

		It("should set ValidationSucceeded to true even with LocalDNS configured", func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					createZoneOverride(".", true),
					createZoneOverride("cluster.local", false),
				},
				KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
					createZoneOverride(".", false),
					createZoneOverride("cluster.local", false),
				},
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeValidationSucceeded)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})
})
