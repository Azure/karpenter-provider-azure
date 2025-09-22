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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/url"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	k8sflag "k8s.io/component-base/cli/flag"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/utils/env"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
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
	ClusterName                    string  `json:"clusterName,omitempty"`
	ClusterEndpoint                string  `json:"clusterEndpoint,omitempty"` // => APIServerName in bootstrap, except needs to be w/o https/port
	VMMemoryOverheadPercent        float64 `json:"vmMemoryOverheadPercent,omitempty"`
	ClusterID                      string  `json:"clusterId,omitempty"`
	KubeletClientTLSBootstrapToken string  `json:"-"` // => TLSBootstrapToken in bootstrap (may need to be per node/nodepool)
	LinuxAdminUsername             string  `json:"-"`
	SSHPublicKey                   string  `json:"-"` // ssh.publicKeys.keyData => VM SSH public key // TODO: move to v1beta1.AKSNodeClass?

	NetworkPlugin     string `json:"networkPlugin,omitempty"`     // => NetworkPlugin in bootstrap
	NetworkPolicy     string `json:"networkPolicy,omitempty"`     // => NetworkPolicy in bootstrap
	NetworkPluginMode string `json:"networkPluginMode,omitempty"` // => Network Plugin Mode is used to control the mode the network plugin should operate in. For example, "overlay" used with --network-plugin=azure will use an overlay network (non-VNET IPs) for pods in the cluster. Learn more about overlay networking here: https://learn.microsoft.com/en-us/azure/aks/azure-cni-overlay?tabs=kubectl#overview-of-overlay-networking
	NetworkDataplane  string `json:"networkDataplane,omitempty"`

	NodeIdentities          []string `json:"nodeIdentities,omitempty"`          // => Applied onto each VM
	KubeletIdentityClientID string   `json:"kubeletIdentityClientID,omitempty"` // => Flows to bootstrap and used in drift
	VnetGUID                string   `json:"vnetGuid,omitempty"`                // resource guid used by azure cni for identifying the right vnet
	SubnetID                string   `json:"subnetId,omitempty"`                // => VnetSubnetID to use (for nodes in Azure CNI Overlay and Azure CNI + pod subnet; for for nodes and pods in Azure CNI), unless overridden via AKSNodeClass
	setFlags                map[string]bool

	ProvisionMode              string            `json:"provisionMode,omitempty"`
	NodeBootstrappingServerURL string            `json:"-"`
	UseSIG                     bool              `json:"useSIG,omitempty"` // => UseSIG is true if Karpenter is managed by AKS, false if it is a self-hosted karpenter installation
	SIGAccessTokenServerURL    string            `json:"-"`                // => SIGAccessTokenServerURL used to access SIG, not set if it is a self-hosted karpenter installation
	SIGSubscriptionID          string            `json:"sigSubscriptionId,omitempty"`
	NodeResourceGroup          string            `json:"nodeResourceGroup,omitempty"`
	AdditionalTags             map[string]string `json:"additionalTags,omitempty"`
	DiskEncryptionSetID        string            `json:"diskEncryptionSetId,omitempty"`
	AKSMachinesReachable       bool              `json:"aksMachinesReachable,omitempty"` // If set to true, existing AKS machines created with PROVISION_MODE=aksmachineapi will be managed even with other provision modes. This option does not have any effect if PROVISION_MODE=aksmachineapi, as it will behave as if this option is set to true.
}

func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	fs.StringVar(&o.ClusterName, "cluster-name", env.WithDefaultString("CLUSTER_NAME", ""), "[REQUIRED] The kubernetes cluster name for resource tags.")
	fs.StringVar(&o.ClusterEndpoint, "cluster-endpoint", env.WithDefaultString("CLUSTER_ENDPOINT", ""), "[REQUIRED] The external kubernetes cluster endpoint for new nodes to connect with.")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", utils.WithDefaultFloat64("VM_MEMORY_OVERHEAD_PERCENT", 0.075), "The VM memory overhead as a percent that will be subtracted from the total memory for all instance types.")
	fs.StringVar(&o.KubeletClientTLSBootstrapToken, "kubelet-bootstrap-token", env.WithDefaultString("KUBELET_BOOTSTRAP_TOKEN", ""), "[REQUIRED] The bootstrap token for new nodes to join the cluster.")
	fs.StringVar(&o.LinuxAdminUsername, "linux-admin-username", env.WithDefaultString("LINUX_ADMIN_USERNAME", "azureuser"), "The admin username for Linux VMs.")
	fs.StringVar(&o.SSHPublicKey, "ssh-public-key", env.WithDefaultString("SSH_PUBLIC_KEY", ""), "[REQUIRED] VM SSH public key.")
	fs.StringVar(&o.NetworkPlugin, "network-plugin", env.WithDefaultString("NETWORK_PLUGIN", consts.NetworkPluginAzure), "The network plugin used by the cluster.")
	fs.StringVar(&o.NetworkPluginMode, "network-plugin-mode", env.WithDefaultString("NETWORK_PLUGIN_MODE", consts.NetworkPluginModeOverlay), "network plugin mode of the cluster.")
	fs.StringVar(&o.NetworkPolicy, "network-policy", env.WithDefaultString("NETWORK_POLICY", ""), "The network policy used by the cluster.")
	fs.StringVar(&o.NetworkDataplane, "network-dataplane", env.WithDefaultString("NETWORK_DATAPLANE", "cilium"), "The network dataplane used by the cluster.")
	fs.StringVar(&o.VnetGUID, "vnet-guid", env.WithDefaultString("VNET_GUID", ""), "The vnet guid of the clusters vnet, only required by azure cni with overlay + byo vnet")
	fs.StringVar(&o.SubnetID, "vnet-subnet-id", env.WithDefaultString("VNET_SUBNET_ID", ""), "[REQUIRED] The default subnet ID to use for new nodes. This must be a valid ARM resource ID for subnet that does not overlap with the service CIDR or the pod CIDR.")
	fs.Var(newNodeIdentitiesValue(env.WithDefaultString("NODE_IDENTITIES", ""), &o.NodeIdentities), "node-identities", "User assigned identities for nodes.")
	fs.StringVar(&o.ProvisionMode, "provision-mode", env.WithDefaultString("PROVISION_MODE", consts.ProvisionModeAKSScriptless), "[UNSUPPORTED] The provision mode for the cluster.")
	fs.StringVar(&o.NodeBootstrappingServerURL, "nodebootstrapping-server-url", env.WithDefaultString("NODEBOOTSTRAPPING_SERVER_URL", ""), "[UNSUPPORTED] The url for the node bootstrapping provider server.")
	fs.StringVar(&o.NodeResourceGroup, "node-resource-group", env.WithDefaultString("AZURE_NODE_RESOURCE_GROUP", ""), "[REQUIRED] the resource group created and managed by AKS where the nodes live")
	fs.StringVar(&o.KubeletIdentityClientID, "kubelet-identity-client-id", env.WithDefaultString("KUBELET_IDENTITY_CLIENT_ID", ""), "The client ID of the kubelet identity.")
	fs.BoolVar(&o.UseSIG, "use-sig", env.WithDefaultBool("USE_SIG", false), "If set to true karpenter will use the AKS managed shared image galleries and the node image versions api. If set to false karpenter will use community image galleries. Only a subset of image features will be available in the community image galleries and this flag is only for the managed node provisioning addon.")
	fs.StringVar(&o.SIGAccessTokenServerURL, "sig-access-token-server-url", env.WithDefaultString("SIG_ACCESS_TOKEN_SERVER_URL", ""), "The URL for the SIG access token server. Only used for AKS managed karpenter. UseSIG must be set tot true for this to take effect.")
	fs.StringVar(&o.SIGSubscriptionID, "sig-subscription-id", env.WithDefaultString("SIG_SUBSCRIPTION_ID", ""), "The subscription ID of the shared image gallery.")
	fs.StringVar(&o.DiskEncryptionSetID, "node-osdisk-diskencryptionset-id", env.WithDefaultString("NODE_OSDISK_DISKENCRYPTIONSET_ID", ""), "The ARM resource ID of the disk encryption set to use for customer-managed key (BYOK) encryption.")
	fs.BoolVar(&o.AKSMachinesReachable, "aks-machines-reachable", env.WithDefaultBool("AKS_MACHINES_REACHABLE", false), "If set to true, existing AKS machines created with PROVISION_MODE=aksmachineapi will be managed even with other provision modes. This option does not have any effect if PROVISION_MODE=aksmachineapi, as it will behave as if this option is set to true.")

	additionalTagsFlag := k8sflag.NewMapStringString(&o.AdditionalTags)
	if err := additionalTagsFlag.Set(env.WithDefaultString("ADDITIONAL_TAGS", "")); err != nil {
		panic(fmt.Sprintf("failed to parse ADDITIONAL_TAGS from string %q: %s", env.WithDefaultString("ADDITIONAL_TAGS", ""), err))
	}
	// See https://github.com/Azure/karpenter-provider-azure/issues/1042 for issue discussing improvements around this
	fs.Var(additionalTagsFlag, "additional-tags", "Additional tags to apply to the resources in Azure. Format is key1=value1,key2=value2. These tags will be merged with the tags specified on the NodePool. In the case of a tag collision, the NodePool tag wins. These tags only apply to new nodes and do not trigger drift, which means that adding tags to this collection will not update existing nodes until drift triggers for some other reason.")
}

func (o *Options) GetAPIServerName() string {
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

func (o *Options) String() string {
	json, err := json.Marshal(o)
	if err != nil {
		return "couldn't marshal options JSON"
	}

	return string(json)
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
