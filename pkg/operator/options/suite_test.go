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

package options_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

var ctx context.Context

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Options")
}

var _ = Describe("Options", func() {
	var envState map[string]string
	var environmentVariables = []string{
		"CLUSTER_NAME",
		"CLUSTER_ENDPOINT",
		"VM_MEMORY_OVERHEAD_PERCENT",
		"CLUSTER_ID",
		"KUBELET_BOOTSTRAP_TOKEN",
		"SSH_PUBLIC_KEY",
		"NETWORK_PLUGIN",
		"NETWORK_POLICY",
		"NODE_IDENTITIES",
		"PROVISION_MODE",
		"NODEBOOTSTRAPPING_SERVER_URL",
		"VNET_GUID",
	}

	var fs *coreoptions.FlagSet
	var opts *options.Options

	BeforeEach(func() {
		envState = map[string]string{}
		for _, ev := range environmentVariables {
			val, ok := os.LookupEnv(ev)
			if ok {
				envState[ev] = val
			}
			os.Unsetenv(ev)
		}
		fs = &coreoptions.FlagSet{
			FlagSet: flag.NewFlagSet("karpenter", flag.ContinueOnError),
		}
		opts = &options.Options{}
		opts.AddFlags(fs)
	})

	AfterEach(func() {
		for _, ev := range environmentVariables {
			os.Unsetenv(ev)
		}
		for ev, val := range envState {
			os.Setenv(ev, val)
		}
	})

	Context("Env Vars", func() {
		It("should correctly fallback to env vars when CLI flags aren't set", func() {
			os.Setenv("CLUSTER_NAME", "env-cluster")
			os.Setenv("CLUSTER_ENDPOINT", "https://environment-cluster-id-value-for-testing")
			os.Setenv("VM_MEMORY_OVERHEAD_PERCENT", "0.3")
			os.Setenv("KUBELET_BOOTSTRAP_TOKEN", "env-bootstrap-token")
			os.Setenv("SSH_PUBLIC_KEY", "env-ssh-public-key")
			os.Setenv("NETWORK_PLUGIN", "none") // Testing with none to make sure the default isn't overriding or something like that with "azure"
			os.Setenv("NETWORK_POLICY", "env-network-policy")
			os.Setenv("NODE_IDENTITIES", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1,/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2")
			os.Setenv("VNET_SUBNET_ID", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub")
			os.Setenv("PROVISION_MODE", "bootstrappingclient")
			os.Setenv("NODEBOOTSTRAPPING_SERVER_URL", "https://nodebootstrapping-server-url")
			os.Setenv("VNET_GUID", "a519e60a-cac0-40b2-b883-084477fe6f5c")
			fs = &coreoptions.FlagSet{
				FlagSet: flag.NewFlagSet("karpenter", flag.ContinueOnError),
			}
			opts.AddFlags(fs)
			err := opts.Parse(fs)
			Expect(err).ToNot(HaveOccurred())
			expectOptionsEqual(opts, test.Options(test.OptionsFields{
				ClusterName:                    lo.ToPtr("env-cluster"),
				ClusterEndpoint:                lo.ToPtr("https://environment-cluster-id-value-for-testing"),
				VMMemoryOverheadPercent:        lo.ToPtr(0.3),
				ClusterID:                      lo.ToPtr("46593302"),
				KubeletClientTLSBootstrapToken: lo.ToPtr("env-bootstrap-token"),
				SSHPublicKey:                   lo.ToPtr("env-ssh-public-key"),
				NetworkPlugin:                  lo.ToPtr("none"),
				NetworkPolicy:                  lo.ToPtr("env-network-policy"),
				SubnetID:                       lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub"),
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2"},
				ProvisionMode:                  lo.ToPtr("bootstrappingclient"),
				NodeBootstrappingServerURL:     lo.ToPtr("https://nodebootstrapping-server-url"),
				VnetGUID:                       lo.ToPtr("a519e60a-cac0-40b2-b883-084477fe6f5c"),
			}))
		})
	})
	Context("Validation", func() {
		It("should fail when vnet guid is not a uuid", func() {
			errMsg := "vnet-guid null is malformed"
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
				"--vnet-guid", "null", // sometimes output of jq can produce null or some other data, we should enforce that the vnet guid passed in at least looks like a uuid
			)
			Expect(err).To(MatchError(ContainSubstring(errMsg)))
		})

		It("should fail when vnet guid is empty for azure cni with overlay clusters", func() {
			errMsg := "vnet-guid cannot be empty for AzureCNI clusters with networkPluginMode overlay"
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
			)
			Expect(err).To(MatchError(ContainSubstring(errMsg)))
		})
		It("should fail when network-plugin-mode is invalid", func() {
			typo := "overlaay"
			errMsg := fmt.Sprintf("network-plugin-mode %v is invalid. network-plugin-mode must equal 'overlay' or ''", typo)

			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
				"--network-plugin-mode", typo,
			)
			Expect(err).To(MatchError(ContainSubstring(errMsg)))
		})
		It("should fail validation when networkDataplane is not valid", func() {
			err := opts.Parse(
				fs,
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--network-dataplane", "ciluum",
			)
			Expect(err).To(MatchError(ContainSubstring("network dataplane ciluum is not a valid network dataplane, valid dataplanes are ('azure', 'cilium')")))
		})
		It("should fail validation when clusterName not included", func() {
			err := opts.Parse(
				fs,
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
			)
			Expect(err).To(MatchError(ContainSubstring("missing field, cluster-name")))
		})
		It("should fail validation when clusterEndpoint not included", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
			)
			Expect(err).To(MatchError(ContainSubstring("missing field, cluster-endpoint")))
		})
		It("should fail validation when kubeletClientTLSBootstrapToken not included", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--ssh-public-key", "flag-ssh-public-key",
			)
			Expect(err).To(MatchError(ContainSubstring("missing field, kubelet-bootstrap-token")))
		})
		It("should fail validation when SSHPublicKey not included", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
			)
			Expect(err).To(MatchError(ContainSubstring("missing field, ssh-public-key")))
		})
		It("should fail validation when VNet SubnetID not included", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "",
			)
			Expect(err).To(MatchError(ContainSubstring("missing field, vnet-subnet-id")))
		})
		It("should fail validation when VNet SubnetID is invalid (not absolute)", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "invalid-vnet-subnet-id",
			)
			Expect(err).To(MatchError(ContainSubstring("vnet-subnet-id is invalid: invalid vnet subnet id: invalid-vnet-subnet-id")))
		})
		It("should fail when clusterEndpoint is invalid (not absolute)", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
			)
			Expect(err).To(MatchError(ContainSubstring("not a valid clusterEndpoint URL")))
		})
		It("should fail when vmMemoryOverheadPercent is negative", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
			)
			Expect(err).To(MatchError(ContainSubstring("vm-memory-overhead-percent cannot be negative")))
		})
		It("should fail when network-plugin is empty", func() {
			errMsg := "network-plugin  is invalid. network-plugin must equal 'azure' or 'none'"

			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--network-plugin", "",
			)
			Expect(err).To(MatchError(ContainSubstring(errMsg)))
		})

		It("should succeed when network-plugin is set to 'azure'", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "azure",
				"--network-plugin-mode", "",
			)
			Expect(err).ToNot(HaveOccurred())
		})
		It("should succeed when network-plugin is set to 'none'", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "none",
			)
			Expect(err).ToNot(HaveOccurred())
		})
		It("should succeed when azure-cni with overlay is configured with the right options", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
				"--vnet-guid", "a519e60a-cac0-40b2-b883-084477fe6f5c",
			)
			Expect(err).ToNot(HaveOccurred())

		})
		It("should fail validation when ProvisionMode is not valid", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--provision-mode", "ekeselfexposed",
			)
			Expect(err).To(MatchError(ContainSubstring("provision-mode")))
		})
		It("should fail validation when ProvisionMode is bootstrappingclient but NodebootstrappingServerURL is not provided", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--provision-mode", "bootstrappingclient",
			)
			Expect(err).To(MatchError(ContainSubstring("nodebootstrapping-server-url")))
		})
	})
})

func expectOptionsEqual(optsA *options.Options, optsB *options.Options) {
	GinkgoHelper()
	Expect(optsA.ClusterName).To(Equal(optsB.ClusterName))
	Expect(optsA.ClusterEndpoint).To(Equal(optsB.ClusterEndpoint))
	Expect(optsA.VMMemoryOverheadPercent).To(Equal(optsB.VMMemoryOverheadPercent))
	Expect(optsA.ClusterID).To(Equal(optsB.ClusterID))
	Expect(optsA.KubeletClientTLSBootstrapToken).To(Equal(optsB.KubeletClientTLSBootstrapToken))
	Expect(optsA.SSHPublicKey).To(Equal(optsB.SSHPublicKey))
	Expect(optsA.NetworkPlugin).To(Equal(optsB.NetworkPlugin))
	Expect(optsA.NetworkPolicy).To(Equal(optsB.NetworkPolicy))
	Expect(optsA.NodeIdentities).To(Equal(optsB.NodeIdentities))
}
