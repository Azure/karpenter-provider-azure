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

package settings_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/settings"
)

var ctx context.Context

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Settings")
}

var _ = Describe("Validation", func() {
	It("should not fail when ConfigMap is nil", func() {
		_, err := (&settings.Settings{}).Inject(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
	})
	It("should succeed to set defaults", func() {
		cm := &v1.ConfigMap{
			Data: map[string]string{
				"azure.clusterEndpoint":                "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"azure.clusterName":                    "my-cluster",
				"azure.clusterID":                      "my-cluster-id",
				"azure.kubeletClientTLSBootstrapToken": "my-bootstrap-token",
				"azure.sshPublicKey":                   "my-ssh-public-key",
				"azure.networkPlugin":                  "kubenet",
				"azure.networkPolicy":                  "azure",
			},
		}
		ctx, err := (&settings.Settings{}).Inject(ctx, cm)
		Expect(err).ToNot(HaveOccurred())
		s := settings.FromContext(ctx)
		Expect(s.VMMemoryOverheadPercent).To(Equal(0.075))
		Expect(len(s.Tags)).To(BeZero())
	})
	It("should succeed to set custom values", func() {
		cm := &v1.ConfigMap{
			Data: map[string]string{
				"azure.clusterEndpoint":                "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"azure.clusterName":                    "my-cluster",
				"azure.vmMemoryOverheadPercent":        "0.1",
				"azure.tags":                           `{"tag1": "value1", "tag2": "value2", "example.com/tag": "my-value"}`,
				"azure.clusterID":                      "my-cluster-id",
				"azure.kubeletClientTLSBootstrapToken": "my-bootstrap-token",
				"azure.sshPublicKey":                   "my-ssh-public-key",
				"azure.networkPlugin":                  "kubenet",
				"azure.networkPolicy":                  "azure",
				"azure.nodeIdentities":                 "[\"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1\",\"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2\"]",
			},
		}
		ctx, err := (&settings.Settings{}).Inject(ctx, cm)
		Expect(err).ToNot(HaveOccurred())
		s := settings.FromContext(ctx)
		Expect(s.VMMemoryOverheadPercent).To(Equal(0.1))
		Expect(len(s.Tags)).To(Equal(3))
		Expect(s.Tags).To(HaveKeyWithValue("tag1", "value1"))
		Expect(s.Tags).To(HaveKeyWithValue("tag2", "value2"))
		Expect(s.Tags).To(HaveKeyWithValue("example.com/tag", "my-value"))
		Expect(s.NodeIdentities).To(HaveLen(2))
		Expect(s.NodeIdentities[0]).To(Equal("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
		Expect(s.NodeIdentities[1]).To(Equal("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2"))
	})
	It("should fail validation with panic when clusterName not included", func() {
		cm := &v1.ConfigMap{
			Data: map[string]string{
				"azure.clusterEndpoint": "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
			},
		}
		_, err := (&settings.Settings{}).Inject(ctx, cm)
		Expect(err).To(HaveOccurred())
	})
	It("should fail validation with panic when clusterEndpoint not included", func() {
		cm := &v1.ConfigMap{
			Data: map[string]string{
				"azure.clusterName": "my-name",
			},
		}
		_, err := (&settings.Settings{}).Inject(ctx, cm)
		Expect(err).To(HaveOccurred())
	})
	It("should fail validation with panic when clusterEndpoint is invalid (not absolute)", func() {
		cm := &v1.ConfigMap{
			Data: map[string]string{
				"azure.clusterName":     "my-name",
				"azure.clusterEndpoint": "karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
			},
		}
		_, err := (&settings.Settings{}).Inject(ctx, cm)
		Expect(err).To(HaveOccurred())
	})
	It("should fail validation with panic when vmMemoryOverheadPercent is negative", func() {
		cm := &v1.ConfigMap{
			Data: map[string]string{
				"azure.clusterEndpoint":         "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"azure.clusterName":             "my-cluster",
				"azure.vmMemoryOverheadPercent": "-0.01",
			},
		}
		_, err := (&settings.Settings{}).Inject(ctx, cm)
		Expect(err).To(HaveOccurred())
	})
	// TODO: more validation tests
})
