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
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/samber/lo"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

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
	"AKS_MACHINES_POOL_NAME",
	"MANAGE_EXISTING_AKS_MACHINES",
}

// saveAndClearEnv saves and unsets all known environment variables, returning a restore function
func saveAndClearEnv(t *testing.T) {
	t.Helper()
	envState := map[string]string{}
	for _, ev := range environmentVariables {
		val, ok := os.LookupEnv(ev)
		if ok {
			envState[ev] = val
		}
		os.Unsetenv(ev)
	}
	t.Cleanup(func() {
		for _, ev := range environmentVariables {
			os.Unsetenv(ev)
		}
		for ev, val := range envState {
			os.Setenv(ev, val)
		}
	})
}

// newFlagSetAndOpts creates a fresh FlagSet and Options for each test
func newFlagSetAndOpts() (*coreoptions.FlagSet, *options.Options) {
	fs := &coreoptions.FlagSet{
		FlagSet: flag.NewFlagSet("karpenter", flag.ContinueOnError),
	}
	opts := &options.Options{}
	opts.AddFlags(fs)
	return fs, opts
}

func TestEnvVarFallback(t *testing.T) {
	saveAndClearEnv(t)

	t.Setenv("CLUSTER_NAME", "env-cluster")
	t.Setenv("CLUSTER_ENDPOINT", "https://environment-cluster-id-value-for-testing")
	t.Setenv("VM_MEMORY_OVERHEAD_PERCENT", "0.3")
	t.Setenv("KUBELET_BOOTSTRAP_TOKEN", "env-bootstrap-token")
	t.Setenv("SSH_PUBLIC_KEY", "env-ssh-public-key")
	t.Setenv("NETWORK_PLUGIN", "none")
	t.Setenv("NETWORK_PLUGIN_MODE", "")
	t.Setenv("NETWORK_POLICY", "env-network-policy")
	t.Setenv("DNS_SERVICE_IP", "10.244.0.1")
	t.Setenv("NODE_IDENTITIES", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1,/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2")
	t.Setenv("VNET_SUBNET_ID", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub")
	t.Setenv("PROVISION_MODE", "bootstrappingclient")
	t.Setenv("NODEBOOTSTRAPPING_SERVER_URL", "https://nodebootstrapping-server-url")
	t.Setenv("USE_SIG", "true")
	t.Setenv("SIG_ACCESS_TOKEN_SERVER_URL", "http://valid-server.com")
	t.Setenv("SIG_SUBSCRIPTION_ID", "my-subscription-id")
	t.Setenv("VNET_GUID", "a519e60a-cac0-40b2-b883-084477fe6f5c")
	t.Setenv("AZURE_NODE_RESOURCE_GROUP", "my-node-rg")
	t.Setenv("KUBELET_IDENTITY_CLIENT_ID", "12345678-1234-1234-1234-123456789012")
	t.Setenv("LINUX_ADMIN_USERNAME", "customadminusername")
	t.Setenv("ADDITIONAL_TAGS", "test-tag=test-value")
	t.Setenv("AKS_MACHINES_POOL_NAME", "testmpool")
	t.Setenv("MANAGE_EXISTING_AKS_MACHINES", "true")

	fs, opts := newFlagSetAndOpts()
	err := opts.Parse(fs)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

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
		KubeletIdentityClientID:        lo.ToPtr("12345678-1234-1234-1234-123456789012"),
		AdditionalTags:                 map[string]string{"test-tag": "test-value"},
		ClusterDNSServiceIP:            lo.ToPtr("10.244.0.1"),
		ManageExistingAKSMachines:      lo.ToPtr(true),
		AKSMachinesPoolName:            lo.ToPtr("testmpool"),
	})

	if diff := cmp.Diff(expectedOpts, opts, cmpopts.IgnoreUnexported(options.Options{})); diff != "" {
		t.Errorf("options mismatch (-want +got):\n%s", diff)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "fail when kubelet-identity-client-id is not a uuid",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--kubelet-identity-client-id", "not-a-uuid",
			},
			wantErr:   true,
			errSubstr: "kubelet-identity-client-id not-a-uuid is malformed",
		},
		{
			name: "fail when vnet guid is not a uuid",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
				"--vnet-guid", "null",
			},
			wantErr:   true,
			errSubstr: "vnet-guid null is malformed",
		},
		{
			name: "fail when network-plugin-mode is invalid",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
				"--network-plugin-mode", "overlaay",
			},
			wantErr:   true,
			errSubstr: "network-plugin-mode overlaay is invalid. network-plugin-mode must equal 'overlay' or ''",
		},
		{
			name: "fail when networkDataplane is not valid",
			args: []string{
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--network-dataplane", "ciluum",
			},
			wantErr:   true,
			errSubstr: "network dataplane ciluum is not a valid network dataplane, valid dataplanes are ('azure', 'cilium')",
		},
		{
			name: "fail when cluster DNS IP is not valid",
			args: []string{
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--dns-service-ip", "999.1.2.3",
			},
			wantErr:   true,
			errSubstr: "dns-service-ip is invalid",
		},
		{
			name: "fail when clusterName not included",
			args: []string{
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
			},
			wantErr:   true,
			errSubstr: "missing field, cluster-name",
		},
		{
			name: "fail when clusterEndpoint not included",
			args: []string{
				"--cluster-name", "my-name",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
			},
			wantErr:   true,
			errSubstr: "missing field, cluster-endpoint",
		},
		{
			name: "fail when kubeletClientTLSBootstrapToken not included",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--ssh-public-key", "flag-ssh-public-key",
			},
			wantErr:   true,
			errSubstr: "missing field, kubelet-bootstrap-token",
		},
		{
			name: "fail when SSHPublicKey not included",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
			},
			wantErr:   true,
			errSubstr: "missing field, ssh-public-key",
		},
		{
			name: "fail when VNet SubnetID not included",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "",
			},
			wantErr:   true,
			errSubstr: "missing field, vnet-subnet-id",
		},
		{
			name: "fail when nodeResourceGroup not included",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
			},
			wantErr:   true,
			errSubstr: "missing field, node-resource-group",
		},
		{
			name: "fail when VNet SubnetID is invalid (not absolute)",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "invalid-vnet-subnet-id",
			},
			wantErr:   true,
			errSubstr: "vnet-subnet-id is invalid: invalid vnet subnet id: invalid-vnet-subnet-id",
		},
		{
			name: "fail when clusterEndpoint is invalid (not absolute)",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
			},
			wantErr:   true,
			errSubstr: "not a valid clusterEndpoint URL",
		},
		{
			name: "fail when vmMemoryOverheadPercent is negative",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vm-memory-overhead-percent", "-0.01",
			},
			wantErr:   true,
			errSubstr: "vm-memory-overhead-percent cannot be negative",
		},
		{
			name: "fail when network-plugin is empty",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--network-plugin", "",
			},
			wantErr:   true,
			errSubstr: "network-plugin  is invalid. network-plugin must equal 'azure' or 'none'",
		},
		{
			name: "fail when networkPluginMode on networkPlugin none",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "none",
				"--network-plugin-mode", "overlay",
			},
			wantErr:   true,
			errSubstr: "network-plugin-mode 'overlay' is invalid when network-plugin is 'none'. network-plugin-mode must be empty",
		},
		{
			name: "succeed with network-plugin azure",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "azure",
				"--network-plugin-mode", "",
				"--node-resource-group", "my-node-rg",
			},
			wantErr: false,
		},
		{
			name: "succeed with network-plugin none",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "none",
				"--network-plugin-mode", "",
				"--node-resource-group", "my-node-rg",
			},
			wantErr: false,
		},
		{
			name: "succeed with azure-cni overlay",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
				"--vnet-guid", "a519e60a-cac0-40b2-b883-084477fe6f5c",
				"--node-resource-group", "my-node-rg",
			},
			wantErr: false,
		},
		{
			name: "fail when ProvisionMode is not valid",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--provision-mode", "ekeselfexposed",
			},
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name: "fail when ProvisionMode bootstrappingclient but no URL",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--provision-mode", "bootstrappingclient",
			},
			wantErr:   true,
			errSubstr: "nodebootstrapping-server-url",
		},
		{
			name: "fail if use-sig without sig-access-token-server-url",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-subscription-id", "my-subscription-id",
				"--use-sig",
			},
			wantErr:   true,
			errSubstr: "sig-access-token-server-url",
		},
		{
			name: "fail if use-sig without sig-subscription-id",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-access-token-server-url", "http://valid-server.com",
				"--use-sig",
			},
			wantErr:   true,
			errSubstr: "sig-subscription-id",
		},
		{
			name: "fail if use-sig with invalid sig-access-token-server-url",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-access-token-server-url", "fake url",
				"--sig-subscription-id", "my-subscription-id",
				"--use-sig",
			},
			wantErr:   true,
			errSubstr: "sig-access-token-server-url",
		},
		{
			name: "fail if use-sig with invalid sig-access-token-scope",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--sig-access-token-server-url", "http://valid-server.com",
				"--sig-access-token-scope", "hfake url",
				"--sig-subscription-id", "my-subscription-id",
				"--use-sig",
			},
			wantErr:   true,
			errSubstr: "sig-access-token-scope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			err := opts.Parse(fs, tt.args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestManageExistingAKSMachines(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantVal bool
	}{
		{
			name: "default to false",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
			},
			wantVal: false,
		},
		{
			name: "set to true",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--manage-existing-aks-machines",
			},
			wantVal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			err := opts.Parse(fs, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.ManageExistingAKSMachines != tt.wantVal {
				t.Errorf("ManageExistingAKSMachines = %v, want %v", opts.ManageExistingAKSMachines, tt.wantVal)
			}
		})
	}
}

func TestAdminUsernameValidation(t *testing.T) {
	baseArgs := []string{
		"--cluster-name", "my-name",
		"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
		"--kubelet-bootstrap-token", "flag-bootstrap-token",
		"--ssh-public-key", "flag-ssh-public-key",
		"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
		"--node-resource-group", "my-node-rg",
	}

	tests := []struct {
		name      string
		username  string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "too long",
			username:  "thisusernameiswaytoolongtobevalid1234567890",
			wantErr:   true,
			errSubstr: "linux-admin-username cannot be longer than 32 characters",
		},
		{
			name:      "doesn't start with a letter",
			username:  "1user",
			wantErr:   true,
			errSubstr: "linux-admin-username must start with a letter and only contain letters, numbers, hyphens, and underscores",
		},
		{
			name:      "invalid characters",
			username:  "user@name",
			wantErr:   true,
			errSubstr: "linux-admin-username must start with a letter and only contain letters, numbers, hyphens, and underscores",
		},
		{
			name:     "valid username",
			username: "valid-user-123",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			args := append(baseArgs, "--linux-admin-username", tt.username)
			err := opts.Parse(fs, args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

//nolint:gocyclo
func TestDiskEncryptionSetValidation(t *testing.T) {
	baseArgs := []string{
		"--cluster-name", "my-name",
		"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
		"--kubelet-bootstrap-token", "flag-bootstrap-token",
		"--ssh-public-key", "flag-ssh-public-key",
		"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
		"--node-resource-group", "my-node-rg",
	}

	tests := []struct {
		name          string
		desID         string
		wantErr       bool
		errSubstr     string
		checkParsedID bool
		wantSubID     string
		wantRG        string
		wantName      string
		wantResType   string
	}{
		{
			name:    "empty disk-encryption-set-id succeeds",
			desID:   "",
			wantErr: false,
		},
		{
			name:          "valid disk-encryption-set-id",
			desID:         "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/my-des",
			wantErr:       false,
			checkParsedID: true,
			wantSubID:     "12345678-1234-1234-1234-123456789012",
			wantRG:        "my-rg",
			wantName:      "my-des",
			wantResType:   "Microsoft.Compute/diskEncryptionSets",
		},
		{
			name:      "incorrect number of segments",
			desID:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg",
			wantErr:   true,
			errSubstr: "expected resource type 'Microsoft.Compute/diskEncryptionSets'",
		},
		{
			name:      "doesn't start with /subscriptions/",
			desID:     "subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/my-des",
			wantErr:   true,
			errSubstr: "invalid DiskEncryptionSet ID",
		},
		{
			name:      "wrong provider",
			desID:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Network/diskEncryptionSets/my-des",
			wantErr:   true,
			errSubstr: "expected resource type 'Microsoft.Compute/diskEncryptionSets'",
		},
		{
			name:      "wrong resource type",
			desID:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/disks/my-disk",
			wantErr:   true,
			errSubstr: "expected resource type 'Microsoft.Compute/diskEncryptionSets'",
		},
		{
			name:      "empty subscription ID",
			desID:     "/subscriptions//resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/my-des",
			wantErr:   true,
			errSubstr: "expected resource type",
		},
		{
			name:      "empty resource group name",
			desID:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups//providers/Microsoft.Compute/diskEncryptionSets/my-des",
			wantErr:   true,
			errSubstr: "expected resource type",
		},
		{
			name:      "empty DES name",
			desID:     "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/diskEncryptionSets/",
			wantErr:   true,
			errSubstr: "invalid DiskEncryptionSet ID",
		},
		{
			name:          "case-insensitive provider names",
			desID:         "/subscriptions/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/my-rg/PROVIDERS/MICROSOFT.COMPUTE/DISKENCRYPTIONSETS/my-des",
			wantErr:       false,
			checkParsedID: true,
			wantSubID:     "12345678-1234-1234-1234-123456789012",
			wantRG:        "my-rg",
			wantName:      "my-des",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			args := baseArgs
			if tt.desID != "" {
				args = append(args, "--node-osdisk-diskencryptionset-id", tt.desID)
			}
			err := opts.Parse(fs, args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkParsedID {
				if opts.ParsedDiskEncryptionSetID == nil {
					t.Fatal("ParsedDiskEncryptionSetID is nil")
				}
				if opts.ParsedDiskEncryptionSetID.SubscriptionID != tt.wantSubID {
					t.Errorf("SubscriptionID = %q, want %q", opts.ParsedDiskEncryptionSetID.SubscriptionID, tt.wantSubID)
				}
				if opts.ParsedDiskEncryptionSetID.ResourceGroupName != tt.wantRG {
					t.Errorf("ResourceGroupName = %q, want %q", opts.ParsedDiskEncryptionSetID.ResourceGroupName, tt.wantRG)
				}
				if opts.ParsedDiskEncryptionSetID.Name != tt.wantName {
					t.Errorf("Name = %q, want %q", opts.ParsedDiskEncryptionSetID.Name, tt.wantName)
				}
				if tt.wantResType != "" && opts.ParsedDiskEncryptionSetID.ResourceType.String() != tt.wantResType {
					t.Errorf("ResourceType = %q, want %q", opts.ParsedDiskEncryptionSetID.ResourceType.String(), tt.wantResType)
				}
			}
		})
	}
}

func TestStringVerification(t *testing.T) {
	t.Run("should have a JSON tag for each expected field", func(t *testing.T) {
		opts := &options.Options{}
		optionsType := reflect.TypeOf(*opts)

		for i := 0; i < optionsType.NumField(); i++ {
			field := optionsType.Field(i)
			fieldName := field.Name

			if !field.IsExported() {
				continue
			}

			jsonTag, hasJSONTag := field.Tag.Lookup("json")

			if jsonTag == "-" {
				continue
			}

			if !hasJSONTag {
				t.Errorf("Field %s should have a JSON tag", fieldName)
				continue
			}

			jsonFieldName := strings.Split(jsonTag, ",")[0]

			if !strings.EqualFold(jsonFieldName, fieldName) {
				t.Errorf("Field %s JSON tag '%s' should match field name (case-insensitive)", fieldName, jsonFieldName)
			}
		}
	})
}

func TestAKSMachineAPI(t *testing.T) {
	tests := []struct {
		name                        string
		args                        []string
		wantErr                     bool
		checkManageExistingMachines *bool
	}{
		{
			name: "succeed with aksmachineapi",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--provision-mode", "aksmachineapi",
				"--aks-machines-pool-name", "testmpool",
				"--use-sig",
				"--sig-subscription-id", "92345678-1234-1234-1234-123456789012",
			},
			wantErr: false,
		},
		{
			name: "succeed with other provision mode",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--provision-mode", "aksscriptless",
				"--aks-machines-pool-name", "unusedpool",
			},
			wantErr: false,
		},
		{
			name: "default manage-existing-aks-machines to false",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
			},
			wantErr:                     false,
			checkManageExistingMachines: lo.ToPtr(false),
		},
		{
			name: "succeed with manage-existing-aks-machines set",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--manage-existing-aks-machines",
			},
			wantErr:                     false,
			checkManageExistingMachines: lo.ToPtr(true),
		},
		{
			name: "allow manage-existing-aks-machines with aksmachineapi",
			args: []string{
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--kubelet-bootstrap-token", "flag-bootstrap-token",
				"--ssh-public-key", "flag-ssh-public-key",
				"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
				"--node-resource-group", "my-node-rg",
				"--manage-existing-aks-machines",
				"--provision-mode", "aksmachineapi",
				"--aks-machines-pool-name", "testmpool",
				"--use-sig",
				"--sig-subscription-id", "92345678-1234-1234-1234-123456789012",
			},
			wantErr:                     false,
			checkManageExistingMachines: lo.ToPtr(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			err := opts.Parse(fs, tt.args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkManageExistingMachines != nil {
				if opts.ManageExistingAKSMachines != *tt.checkManageExistingMachines {
					t.Errorf("ManageExistingAKSMachines = %v, want %v", opts.ManageExistingAKSMachines, *tt.checkManageExistingMachines)
				}
			}
		})
	}
}

func TestAdditionalTagsValidation(t *testing.T) {
	baseArgs := []string{
		"--cluster-name", "my-name",
		"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
		"--kubelet-bootstrap-token", "flag-bootstrap-token",
		"--ssh-public-key", "flag-ssh-public-key",
		"--vnet-subnet-id", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub",
		"--node-resource-group", "my-node-rg",
	}

	tests := []struct {
		name      string
		extraArgs []string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "valid additional tags",
			extraArgs: []string{"--additional-tags", "env=prod,team=platform,version=1.0"},
			wantErr:   false,
		},
		{
			name:      "key exceeds 512 characters",
			extraArgs: []string{"--additional-tags", fmt.Sprintf("%s=value", strings.Repeat("a", 513))},
			wantErr:   true,
			errSubstr: "exceeds maximum length of 512 characters",
		},
		{
			name:      "value exceeds 256 characters",
			extraArgs: []string{"--additional-tags", fmt.Sprintf("key=%s", strings.Repeat("b", 257))},
			wantErr:   true,
			errSubstr: "exceeds maximum length of 256 characters",
		},
		{
			name:      "maximum allowed lengths succeed",
			extraArgs: []string{"--additional-tags", fmt.Sprintf("%s=%s", strings.Repeat("a", 512), strings.Repeat("b", 256))},
			wantErr:   false,
		},
		{
			name: "malformed additional-tags",
			extraArgs: []string{
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
				"--vnet-guid", "a519e60a-cac0-40b2-b883-084477fe6f5c",
				"--additional-tags", "key1/value2",
			},
			wantErr:   true,
			errSubstr: "invalid value \"key1/value2\" for flag -additional-tags: malformed pair, expect string=string",
		},
		{
			name: "duplicate keys",
			extraArgs: []string{
				"--network-plugin", "azure",
				"--network-plugin-mode", "overlay",
				"--vnet-guid", "a519e60a-cac0-40b2-b883-084477fe6f5c",
				"--additional-tags", "key1=value1,key2=value2,KEY1=value3",
			},
			wantErr:   true,
			errSubstr: "is not unique (case-insensitive). Duplicate key found",
		},
	}

	// Test invalid characters in key separately since it iterates
	invalidChars := []string{"<", ">", "%", "&", "\\", "?", "/"}
	for _, char := range invalidChars {
		tests = append(tests, struct {
			name      string
			extraArgs []string
			wantErr   bool
			errSubstr string
		}{
			name:      fmt.Sprintf("invalid character %q in key", char),
			extraArgs: []string{"--additional-tags", fmt.Sprintf("key%sname=value", char)},
			wantErr:   true,
			errSubstr: "contains invalid characters",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			args := append(baseArgs, tt.extraArgs...)
			err := opts.Parse(fs, args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestAzureVMProvisionMode(t *testing.T) {
	t.Parallel()

	azurevmSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub"

	tests := []struct {
		name      string
		args      []string
		wantErr   bool
		errSubstr string
		validate  func(t *testing.T, opts *options.Options)
	}{
		{
			name: "should succeed with only subnet and node-resource-group in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.ProvisionMode != "azurevm" {
					t.Errorf("expected provision mode 'azurevm', got %q", opts.ProvisionMode)
				}
			},
		},
		{
			name: "should not require cluster-endpoint in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
		},
		{
			name: "should not require cluster-name in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.ClusterName != "" {
					t.Errorf("expected empty ClusterName, got %q", opts.ClusterName)
				}
			},
		},
		{
			name: "should not require kubelet-bootstrap-token in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.KubeletClientTLSBootstrapToken != "" {
					t.Errorf("expected empty KubeletClientTLSBootstrapToken, got %q", opts.KubeletClientTLSBootstrapToken)
				}
			},
		},
		{
			name: "should not require ssh-public-key in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.SSHPublicKey != "" {
					t.Errorf("expected empty SSHPublicKey, got %q", opts.SSHPublicKey)
				}
			},
		},
		{
			name:      "should fail in azurevm mode when vnet-subnet-id is missing",
			args:      []string{"--provision-mode", "azurevm", "--vnet-subnet-id", "", "--node-resource-group", "my-node-rg"},
			wantErr:   true,
			errSubstr: "missing field, vnet-subnet-id",
		},
		{
			name:      "should fail in azurevm mode when node-resource-group is missing",
			args:      []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID},
			wantErr:   true,
			errSubstr: "missing field, node-resource-group",
		},
		{
			name:      "should still validate vnet-subnet-id format in azurevm mode",
			args:      []string{"--provision-mode", "azurevm", "--vnet-subnet-id", "invalid-subnet-id", "--node-resource-group", "my-node-rg"},
			wantErr:   true,
			errSubstr: "vnet-subnet-id is invalid",
		},
		{
			name:      "should still validate vm-memory-overhead-percent in azurevm mode",
			args:      []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg", "--vm-memory-overhead-percent", "-0.01"},
			wantErr:   true,
			errSubstr: "vm-memory-overhead-percent cannot be negative",
		},
		{
			name:      "should still validate additional-tags in azurevm mode",
			args:      []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg", "--additional-tags", fmt.Sprintf("%s=value", strings.Repeat("a", 513))},
			wantErr:   true,
			errSubstr: "exceeds maximum length of 512 characters",
		},
		{
			name:      "should still validate disk-encryption-set-id in azurevm mode",
			args:      []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg", "--node-osdisk-diskencryptionset-id", "not-a-valid-resource-id"},
			wantErr:   true,
			errSubstr: "invalid DiskEncryptionSet ID",
		},
		{
			name: "should not generate ClusterID when ClusterEndpoint is empty in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.ClusterID != "" {
					t.Errorf("expected empty ClusterID, got %q", opts.ClusterID)
				}
			},
		},
		{
			name: "should succeed with optional cluster-endpoint and generate ClusterID in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg", "--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.ClusterID == "" {
					t.Error("expected non-empty ClusterID when cluster-endpoint is provided")
				}
			},
		},
		{
			name: "should skip networking validation in azurevm mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg", "--network-plugin", "kubenet"},
		},
		{
			name: "IsAzureVMMode returns true for azurevm provision mode",
			args: []string{"--provision-mode", "azurevm", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if !opts.IsAzureVMMode() {
					t.Error("expected IsAzureVMMode() to return true")
				}
			},
		},
		{
			name: "IsAzureVMMode returns false for aksscriptless provision mode",
			args: []string{"--cluster-name", "my-name", "--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io", "--kubelet-bootstrap-token", "flag-bootstrap-token", "--ssh-public-key", "flag-ssh-public-key", "--vnet-subnet-id", azurevmSubnetID, "--node-resource-group", "my-node-rg"},
			validate: func(t *testing.T, opts *options.Options) {
				if opts.IsAzureVMMode() {
					t.Error("expected IsAzureVMMode() to return false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saveAndClearEnv(t)
			fs, opts := newFlagSetAndOpts()
			err := opts.Parse(fs, tt.args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.validate != nil {
				tt.validate(t, opts)
			}
		})
	}
}
