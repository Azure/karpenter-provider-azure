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

package options

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/utils/env"
)

func init() {
	coreoptions.Injectables = append(coreoptions.Injectables, &Options{})
}

type nodeIdentitiesValue []string

func newNodeIdentitiesValue(val string, p *[]string) *nodeIdentitiesValue {
	*p = []string{}
	if val != "" {
		*p = strings.Split(val, ",")
	}
	return (*nodeIdentitiesValue)(p)
}

func (s *nodeIdentitiesValue) Set(val string) error {
	*s = nodeIdentitiesValue(strings.Split(val, ","))
	return nil
}

func (s *nodeIdentitiesValue) Get() any { return []string(*s) }

func (s *nodeIdentitiesValue) String() string { return strings.Join(*s, ",") }

type optionsKey struct{}

// Options contains the configuration provided by the user.
// Currently we do not support changing these after initialization in any way.
// Even if we always get the updated value from the context/pointer, their copies that have been passed into external/vendored functions will not be updated.
// And some of their defaults might even depends on neighbouring fields (e.g., APIServerName).
// So, if one day we want to support dynamic configuration updates, consider creating the a new instance of Options and reinitialize the operator/controllers.
//
// If some fields need to be updated/refreshed and have their own (valid) cascading update procedure, consider moving it away to prevent confusion with the above assumption.
// At that point, we should consider having a more clear distinction between user options and global variables.
type Options struct {
	// Target cluster information; might be use for both bootstrapping and ARM authentications
	Cloud           string
	Location        string
	TenantID        string
	SubscriptionID  string
	ResourceGroup   string
	ClusterName     string
	ClusterEndpoint string // => APIServerName in bootstrap, except needs to be w/o https/port
	ClusterID       string
	APIServerName   string

	// Node parameters
	NodeResourceGroup              string
	KubeletIdentityClientID        string
	KubeletClientTLSBootstrapToken string   // => TLSBootstrapToken in bootstrap (may need to be per node/nodepool)
	NodeIdentities                 []string // => Applied onto each VM
	SSHPublicKey                   string   // ssh.publicKeys.keyData => VM SSH public key // TODO: move to v1alpha2.AKSNodeClass?
	NetworkPlugin                  string   // => NetworkPlugin in bootstrap
	NetworkPolicy                  string   // => NetworkPolicy in bootstrap
	SubnetID                       string   // => VnetSubnetID to use (for nodes in Azure CNI Overlay and Azure CNI + pod subnet; for for nodes and pods in Azure CNI), unless overridden via AKSNodeClass
	VnetGUID                       string

	// Behavioral configuration
	ArmAuthMethod           string
	VMMemoryOverheadPercent float64
}

func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	fs.StringVar(&o.Cloud, "cloud", env.WithDefaultString("ARM_CLOUD", "AZUREPUBLICCLOUD"), "The cloud environment to use. Currently only supports 'AZUREPUBLICCLOUD'.")
	fs.StringVar(&o.Location, "location", env.WithDefaultString("LOCATION", ""), "[REQUIRED] The location of the cluster.")
	fs.StringVar(&o.TenantID, "tenant-id", env.WithDefaultString("ARM_TENANT_ID", ""), "The tenant ID of the cluster.")
	fs.StringVar(&o.SubscriptionID, "subscription-id", env.WithDefaultString("ARM_SUBSCRIPTION_ID", ""), "[REQUIRED] The subscription ID of the cluster.")
	fs.StringVar(&o.ResourceGroup, "resource-group", env.WithDefaultString("ARM_RESOURCE_GROUP", ""), "The resource group of the cluster.")
	fs.StringVar(&o.ClusterName, "cluster-name", env.WithDefaultString("CLUSTER_NAME", ""), "[REQUIRED] The kubernetes cluster name for resource tags.")
	fs.StringVar(&o.ClusterEndpoint, "cluster-endpoint", env.WithDefaultString("CLUSTER_ENDPOINT", ""), "[REQUIRED] The external kubernetes cluster endpoint for new nodes to connect with.")

	fs.StringVar(&o.NodeResourceGroup, "node-resource-group", env.WithDefaultString("AZURE_NODE_RESOURCE_GROUP", ""), "[REQUIRED] The resource group of the nodes.")
	fs.StringVar(&o.KubeletClientTLSBootstrapToken, "kubelet-bootstrap-token", env.WithDefaultString("KUBELET_BOOTSTRAP_TOKEN", ""), "[REQUIRED] The bootstrap token for new nodes to join the cluster.")
	fs.StringVar(&o.KubeletIdentityClientID, "kubelet-identity-client-id", env.WithDefaultString("ARM_KUBELET_IDENTITY_CLIENT_ID", ""), "[REQUIRED] The client ID of the user assigned identity for kubelet.")
	fs.Var(newNodeIdentitiesValue(env.WithDefaultString("NODE_IDENTITIES", ""), &o.NodeIdentities), "node-identities", "Additional identities to be assigned to the provisioned VMs. Allow support for AKS features like Addons.")
	fs.StringVar(&o.SSHPublicKey, "ssh-public-key", env.WithDefaultString("SSH_PUBLIC_KEY", ""), "[REQUIRED] VM SSH public key.")
	fs.StringVar(&o.NetworkPlugin, "network-plugin", env.WithDefaultString("NETWORK_PLUGIN", "azure"), "The network plugin used by the cluster.")
	fs.StringVar(&o.NetworkPolicy, "network-policy", env.WithDefaultString("NETWORK_POLICY", ""), "The network policy used by the cluster.")
	fs.StringVar(&o.SubnetID, "vnet-subnet-id", env.WithDefaultString("VNET_SUBNET_ID", ""), "[REQUIRED] The default subnet ID to use for new nodes. This must be a valid ARM resource ID for subnet that does not overlap with the service CIDR or the pod CIDR")

	fs.StringVar(&o.ArmAuthMethod, "auth-method", env.WithDefaultString("ARM_AUTH_METHOD", "workload-identity"), "The authentication method to use.")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", env.WithDefaultFloat64("VM_MEMORY_OVERHEAD_PERCENT", 0.075), "The VM memory overhead as a percent that will be subtracted from the total memory for all instance types.")
}

func (o *Options) Parse(fs *coreoptions.FlagSet, args ...string) error {
	ctx := context.Background()

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		return fmt.Errorf("parsing flags, %w", err)
	}

	if err := o.Validate(); err != nil {
		return fmt.Errorf("validating options, %w", err)
	}

	if err := o.Default(ctx); err != nil {
		return fmt.Errorf("defaulting options, %w", err)
	}

	return nil
}

func (o *Options) ToContext(ctx context.Context) context.Context {
	return ToContext(ctx, o)
}

func ToContext(ctx context.Context, opts *Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

func FromContext(ctx context.Context) *Options {
	retval := ctx.Value(optionsKey{})
	if retval == nil {
		return nil
	}
	return retval.(*Options)
}
