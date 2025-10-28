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
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
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
		"DNS_SERVICE_IP",
		"NODE_IDENTITIES",
		"PROVISION_MODE",
		"NODEBOOTSTRAPPING_SERVER_URL",
		"VNET_GUID",
		"USE_SIG",
		"SIG_ACCESS_TOKEN_SERVER_URL",
		"SIG_ACCESS_TOKEN_SCOPE",
		"SIG_SUBSCRIPTION_ID",
		"AZURE_NODE_RESOURCE_GROUP",
		"KUBELET_IDENTITY_CLIENT_ID",
		"LINUX_ADMIN_USERNAME",
		"ADDITIONAL_TAGS",
		"ENABLE_AZURE_SDK_LOGGING",
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
			os.Setenv("NETWORK_PLUGIN_MODE", "")
			os.Setenv("NETWORK_POLICY", "env-network-policy")
			os.Setenv("DNS_SERVICE_IP", "10.244.0.1")
			os.Setenv("NODE_IDENTITIES", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1,/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2")
			os.Setenv("VNET_SUBNET_ID", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub")
			os.Setenv("PROVISION_MODE", "bootstrappingclient")
			os.Setenv("NODEBOOTSTRAPPING_SERVER_URL", "https://nodebootstrapping-server-url")
			os.Setenv("USE_SIG", "true")
			os.Setenv("SIG_ACCESS_TOKEN_SERVER_URL", "http://valid-server.com")
			os.Setenv("SIG_SUBSCRIPTION_ID", "my-subscription-id")
			os.Setenv("VNET_GUID", "a519e60a-cac0-40b2-b883-084477fe6f5c")
			os.Setenv("AZURE_NODE_RESOURCE_GROUP", "my-node-rg")
			os.Setenv("KUBELET_IDENTITY_CLIENT_ID", "2345678-1234-1234-1234-123456789012")
			os.Setenv("LINUX_ADMIN_USERNAME", "customadminusername")
			os.Setenv("ADDITIONAL_TAGS", "test-tag=test-value")
			fs = &coreoptions.FlagSet{
				FlagSet: flag.NewFlagSet("karpenter", flag.ContinueOnError),
			}
			opts.AddFlags(fs)
			err := opts.Parse(fs)
			Expect(err).ToNot(HaveOccurred())
			expectedOpts := test.Options(test.OptionsFields{
				ClusterName:                    lo.ToPtr("env-cluster"),
				ClusterEndpoint:                lo.ToPtr("https://environment-cluster-id-value-for-testing"),
				VMMemoryOverheadPercent:        lo.ToPtr(0.3),
				ClusterID:                      lo.ToPtr("46593302"),
				KubeletClientTLSBootstrapToken: lo.ToPtr("env-bootstrap-token"),
				LinuxAdminUsername:             lo.ToPtr("customadminusername"),
				SSHPublicKey:                   lo.ToPtr("env-ssh-public-key"),
				NetworkPlugin:                  lo.ToPtr("none"),
				NetworkPluginMode:              lo.ToPtr(""),
				NetworkPolicy:                  lo.ToPtr("env-network-policy"),
				SubnetID:                       lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub"),
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2"},
				ProvisionMode:                  lo.ToPtr("bootstrappingclient"),
				NodeBootstrappingServerURL:     lo.ToPtr("https://nodebootstrapping-server-url"),
				VnetGUID:                       lo.ToPtr("a519e60a-cac0-40b2-b883-084477fe6f5c"),
				UseSIG:                         lo.ToPtr(true),
				SIGAccessTokenServerURL:        lo.ToPtr("http://valid-server.com"),
				SIGSubscriptionID:              lo.ToPtr("my-subscription-id"),
				NodeResourceGroup:              lo.ToPtr("my-node-rg"),
				KubeletIdentityClientID:        lo.ToPtr("2345678-1234-1234-1234-123456789012"),
				AdditionalTags:                 map[string]string{"test-tag": "test-value"},
				ClusterDNSServiceIP:            lo.ToPtr("10.244.0.1"),
			})
			Expect(opts).To(BeComparableTo(expectedOpts, cmpopts.IgnoreUnexported(options.Options{})))
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
		It("should fail validation when cluster DNS IP is not valid", func() {
			err := opts.Parse(
				fs,
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--dns-service-ip", "999.1.2.3",
			)
			Expect(err).To(MatchError(ContainSubstring("dns-service-ip is invalid")))
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
		It("should fail validation when nodeResourceGroup not included", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
			)
			Expect(err).To(MatchError(ContainSubstring("missing field, node-resource-group")))
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

		It("should fail when networkPluginMode is specified on a networkPluginMode none cluster", func() {
			errMsg := "network-plugin-mode 'overlay' is invalid when network-plugin is 'none'. network-plugin-mode must be empty"
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "none",
				"--network-plugin-mode", "overlay",
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
				"--node-resource-group", "my-node-rg",
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
				"--network-plugin-mode", "",
				"--node-resource-group", "my-node-rg",
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
				"--node-resource-group", "my-node-rg",
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
			Expect(err).To(MatchError(ContainSubstring("invalid")))
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
		It("should fail if use-sig is enabled, but sig-access-token-server-url is not set", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-subscription-id", "my-subscription-id",
				"--use-sig",
			)
			Expect(err).To(MatchError(ContainSubstring("sig-access-token-server-url")))
		})
		It("should fail if use-sig is enabled, but sig-subscription-id is not set", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-access-token-server-url", "http://valid-server.com",
				"--use-sig",
			)
			Expect(err).To(MatchError(ContainSubstring("sig-subscription-id")))
		})
		It("should fail if use-sig is enabled, but sig-access-token-server-url is invalid URL", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-access-token-server-url", "fake url",
				"--sig-subscription-id", "my-subscription-id",
				"--use-sig",
			)
			Expect(err).To(MatchError(ContainSubstring("sig-access-token-server-url")))
		})
		It("should fail if use-sig is enabled, but sig-access-token-scope is invalid URL", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-access-token-server-url", "http://valid-server.com",
				"--sig-access-token-scope", "hfake url",
				"--sig-subscription-id", "my-subscription-id",
				"--use-sig",
			)
			Expect(err).To(MatchError(ContainSubstring("sig-access-token-scope")))
		})
		It("should fail if additional-tags is malformed", func() {
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
				"--node-resource-group", "my-node-rg",
				"--additional-tags", "key1/value2",
			)
			Expect(err).To(MatchError(ContainSubstring("invalid value \"key1/value2\" for flag -additional-tags: malformed pair, expect string=string")))
		})
		It("should fail if additional-tags has duplicate keys", func() {
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
				"--node-resource-group", "my-node-rg",
				"--additional-tags", "key1=value1,key2=value2,KEY1=value3",
			)
			Expect(err).To(MatchError(ContainSubstring("is not unique (case-insensitive). Duplicate key found")))
		})
		It("should fail if additional-tags has invalid character", func() {
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
				"--node-resource-group", "my-node-rg",
				"--additional-tags", "<key1>=value1,",
			)
			Expect(err).To(MatchError(ContainSubstring("validating options, additional-tags key \"<key1>\" contains invalid characters.")))
		})
	})

	Context("Admin Username Validation", func() {
		It("should fail when linux-admin-username is too long", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--linux-admin-username", "thisusernameiswaytoolongtobevalid1234567890",
			)
			Expect(err).To(MatchError(ContainSubstring("linux-admin-username cannot be longer than 32 characters")))
		})

		It("should fail when linux-admin-username doesn't start with a letter", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--linux-admin-username", "1user",
			)
			Expect(err).To(MatchError(ContainSubstring("linux-admin-username must start with a letter and only contain letters, numbers, hyphens, and underscores")))
		})

		It("should fail when linux-admin-username contains invalid characters", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--linux-admin-username", "user@name",
			)
			Expect(err).To(MatchError(ContainSubstring("linux-admin-username must start with a letter and only contain letters, numbers, hyphens, and underscores")))
		})

		It("should succeed with valid linux-admin-username", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--linux-admin-username", "valid-user-123",
			)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("DiskEncryptionSet Validation", func() {
		It("should succeed when disk-encryption-set-id is empty", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
			)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should succeed with valid disk-encryption-set-id", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/my-des",
			)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail when disk-encryption-set-id has incorrect number of segments", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg",
			)
			Expect(err).To(MatchError(ContainSubstring("disk-encryption-set-id is invalid: expected format")))
		})

		It("should fail when disk-encryption-set-id doesn't start with /subscriptions/", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/my-des",
			)
			Expect(err).To(MatchError(ContainSubstring("disk-encryption-set-id is invalid: must start with /subscriptions/")))
		})

		It("should fail when disk-encryption-set-id has wrong provider", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Network/diskEncryptionSets/my-des",
			)
			Expect(err).To(MatchError(ContainSubstring("disk-encryption-set-id is invalid: expected 'providers/Microsoft.Compute'")))
		})

		It("should fail when disk-encryption-set-id has wrong resource type", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/disks/my-disk",
			)
			Expect(err).To(MatchError(ContainSubstring("disk-encryption-set-id is invalid: expected 'diskEncryptionSets'")))
		})

		It("should fail when disk-encryption-set-id has empty subscription ID", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "/subscriptions//resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/my-des",
			)
			Expect(err).To(MatchError(ContainSubstring("disk-encryption-set-id is invalid: subscription ID, resource group name, and disk encryption set name must not be empty")))
		})

		It("should succeed with case-insensitive provider names", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--node-osdisk-diskencryptionset-id", "/subscriptions/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/my-rg/PROVIDERS/MICROSOFT.COMPUTE/DISKENCRYPTIONSETS/my-des",
			)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("String verification", func() {
		It("should have a JSON tag for each expected field", func() {
			opts := &options.Options{}

			// Use reflection to get the type information of the Options struct
			optionsType := reflect.TypeOf(*opts)

			// Iterate through all fields in the struct
			for i := 0; i < optionsType.NumField(); i++ {
				field := optionsType.Field(i)
				fieldName := field.Name

				// Skip unexported fields (those starting with lowercase)
				if !field.IsExported() {
					continue
				}

				// Get the JSON tag
				jsonTag, hasJSONTag := field.Tag.Lookup("json")

				// Handle fields that should be excluded from JSON (tagged with "-")
				if jsonTag == "-" {
					// These fields are intentionally excluded from JSON serialization
					// Examples: KubeletClientTLSBootstrapToken, LinuxAdminUsername, SSHPublicKey, etc.
					continue
				}

				// For fields that should have JSON tags, verify they exist and match
				Expect(hasJSONTag).To(BeTrue(), "Field %s should have a JSON tag", fieldName)

				// Parse the JSON tag (it might have options like "omitempty")
				jsonFieldName := strings.Split(jsonTag, ",")[0]

				// Verify the JSON tag matches the field name (case-insensitive)
				Expect(strings.ToLower(jsonFieldName)).To(Equal(strings.ToLower(fieldName)),
					"Field %s JSON tag '%s' should match field name (case-insensitive)", fieldName, jsonFieldName)
			}
		})
	})
})
