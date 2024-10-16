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
	"hash/fnv"
	"math/rand"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"k8s.io/apimachinery/pkg/util/sets"
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

type Options struct {
	ClusterName                    string
	ClusterEndpoint                string // => APIServerName in bootstrap, except needs to be w/o https/port
	VMMemoryOverheadPercent        float64
	ClusterID                      string
	KubeletClientTLSBootstrapToken string // => TLSBootstrapToken in bootstrap (may need to be per node/nodepool)
	SSHPublicKey                   string // ssh.publicKeys.keyData => VM SSH public key // TODO: move to v1alpha2.AKSNodeClass?
	NetworkPlugin                  string // => NetworkPlugin in bootstrap
	NetworkPolicy                  string // => NetworkPolicy in bootstrap
	NetworkPluginMode              string // => Network Plugin Mode is used to control the mode the network plugin should operate in. For example, "overlay" used with --network-plugin=azure will use an overlay network (non-VNET IPs) for pods in the cluster. Learn more about overlay networking here: https://learn.microsoft.com/en-us/azure/aks/azure-cni-overlay?tabs=kubectl#overview-of-overlay-networking
	NetworkDataplane               string
	NodeIdentities                 []string // => Applied onto each VM

	SubnetID string // => VnetSubnetID to use (for nodes in Azure CNI Overlay and Azure CNI + pod subnet; for for nodes and pods in Azure CNI), unless overridden via AKSNodeClass
	setFlags map[string]bool

	ProvisionMode              string
	NodeBootstrappingServerURL string
	ManagedKarpenter           bool // => ManagedKarpenter is true if Karpenter is managed by AKS, false if it is a self-hosted karpenter installation

	SIGSubscriptionID string

	NodeResourceGroup string
}

func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	fs.StringVar(&o.ClusterName, "cluster-name", env.WithDefaultString("CLUSTER_NAME", ""), "[REQUIRED] The kubernetes cluster name for resource tags.")
	fs.StringVar(&o.ClusterEndpoint, "cluster-endpoint", env.WithDefaultString("CLUSTER_ENDPOINT", ""), "[REQUIRED] The external kubernetes cluster endpoint for new nodes to connect with.")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", utils.WithDefaultFloat64("VM_MEMORY_OVERHEAD_PERCENT", 0.075), "The VM memory overhead as a percent that will be subtracted from the total memory for all instance types.")
	fs.StringVar(&o.KubeletClientTLSBootstrapToken, "kubelet-bootstrap-token", env.WithDefaultString("KUBELET_BOOTSTRAP_TOKEN", ""), "[REQUIRED] The bootstrap token for new nodes to join the cluster.")
	fs.StringVar(&o.SSHPublicKey, "ssh-public-key", env.WithDefaultString("SSH_PUBLIC_KEY", ""), "[REQUIRED] VM SSH public key.")
	fs.StringVar(&o.NetworkPlugin, "network-plugin", env.WithDefaultString("NETWORK_PLUGIN", consts.NetworkPluginAzure), "The network plugin used by the cluster.")
	fs.StringVar(&o.NetworkPluginMode, "network-plugin-mode", env.WithDefaultString("NETWORK_PLUGIN_MODE", consts.NetworkPluginModeOverlay), "network plugin mode of the cluster.")
	fs.StringVar(&o.NetworkPolicy, "network-policy", env.WithDefaultString("NETWORK_POLICY", ""), "The network policy used by the cluster.")
	fs.StringVar(&o.NetworkDataplane, "network-dataplane", env.WithDefaultString("NETWORK_DATAPLANE", "cilium"), "The network dataplane used by the cluster.")
	fs.StringVar(&o.SubnetID, "vnet-subnet-id", env.WithDefaultString("VNET_SUBNET_ID", ""), "The default subnet ID to use for new nodes. This must be a valid ARM resource ID for subnet that does not overlap with the service CIDR or the pod CIDR.")
	fs.Var(newNodeIdentitiesValue(env.WithDefaultString("NODE_IDENTITIES", ""), &o.NodeIdentities), "node-identities", "User assigned identities for nodes.")
	fs.StringVar(&o.NodeResourceGroup, "node-resource-group", env.WithDefaultString("AZURE_NODE_RESOURCE_GROUP", ""), "[REQUIRED] the resource group created and managed by AKS where the nodes live.")
	fs.StringVar(&o.ProvisionMode, "provision-mode", env.WithDefaultString("PROVISION_MODE", consts.ProvisionModeAKSScriptless), "[UNSUPPORTED] The provision mode for the cluster.")
	fs.StringVar(&o.NodeBootstrappingServerURL, "nodebootstrapping-server-url", env.WithDefaultString("NODEBOOTSTRAPPING_SERVER_URL", ""), "[UNSUPPORTED] The url for the node bootstrapping provider server.")
	fs.BoolVar(&o.ManagedKarpenter, "managed-karpenter", env.WithDefaultBool("MANAGED_KARPENTER", false), "Whether Karpenter is managed by AKS or not.")
	fs.StringVar(&o.SIGSubscriptionID, "sig-subscription-id", env.WithDefaultString("SIG_SUBSCRIPTION_ID", ""), "The subscription ID of the shared image gallery.")
}

func (o Options) GetAPIServerName() string {
	endpoint, _ := url.Parse(o.ClusterEndpoint) // assume to already validated
	return endpoint.Hostname()
}

func (o *Options) Parse(fs *coreoptions.FlagSet, args ...string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		return fmt.Errorf("parsing flags, %w", err)
	}

	// Check if each option has been set. This is a little brute force and better options might exist,
	// but this only needs to be here for one version
	o.setFlags = map[string]bool{}
	cliFlags := sets.New[string]()
	fs.Visit(func(f *flag.Flag) {
		cliFlags.Insert(f.Name)
	})
	fs.VisitAll(func(f *flag.Flag) {
		envName := strings.ReplaceAll(strings.ToUpper(f.Name), "-", "_")
		_, ok := os.LookupEnv(envName)
		o.setFlags[f.Name] = ok || cliFlags.Has(f.Name)
	})

	if err := o.Validate(); err != nil {
		return fmt.Errorf("validating options, %w", err)
	}

	// ClusterID is generated from cluster endpoint
	o.ClusterID = getAKSClusterID(o.GetAPIServerName())

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

// getAKSClusterID returns cluster ID based on the DNS prefix of the cluster.
// The logic comes from AgentBaker and other places, originally from aks-engine
// with the additional assumption of DNS prefix being the first 33 chars of FQDN
func getAKSClusterID(apiServerFQDN string) string {
	dnsPrefix := apiServerFQDN[:33]
	h := fnv.New64a()
	h.Write([]byte(dnsPrefix))
	r := rand.New(rand.NewSource(int64(h.Sum64()))) //nolint:gosec
	return fmt.Sprintf("%08d", r.Uint32())[:8]
}
