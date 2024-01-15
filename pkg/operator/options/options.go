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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/settings"
	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"
	"github.com/aws/karpenter-core/pkg/utils/env"
	"k8s.io/apimachinery/pkg/util/sets"
)

func init() {
	coreoptions.Injectables = append(coreoptions.Injectables, &Options{})
}

type nodeIdentitiesValue []string

func newNodeIdentitiesValue(val string, p *[]string) *nodeIdentitiesValue {
	*p = strings.Split(val, ",")
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
	KubeletClientTLSBootstrapToken string   // => TLSBootstrapToken in bootstrap (may need to be per node/nodepool)
	SSHPublicKey                   string   // ssh.publicKeys.keyData => VM SSH public key // TODO: move to node template?
	NetworkPlugin                  string   // => NetworkPlugin in bootstrap
	NetworkPolicy                  string   // => NetworkPolicy in bootstrap
	NodeIdentities                 []string // => Applied onto each VM

	setFlags map[string]bool
}

func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	fs.StringVar(&o.ClusterName, "cluster-name", env.WithDefaultString("CLUSTER_NAME", ""), "[REQUIRED] The kubernetes cluster name for resource discovery.")
	fs.StringVar(&o.ClusterEndpoint, "cluster-endpoint", env.WithDefaultString("CLUSTER_ENDPOINT", ""), "[REQUIRED] The external kubernetes cluster endpoint for new nodes to connect with. If not specified, will discover the cluster endpoint using DescribeCluster API.")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", env.WithDefaultFloat64("VM_MEMORY_OVERHEAD_PERCENT", 0.075), "The VM memory overhead as a percent that will be subtracted from the total memory for all instance types.")
	fs.StringVar(&o.ClusterID, "cluster-id", env.WithDefaultString("CLUSTER_ID", ""), "The kubernetes cluster ID. If not specified, will generated based on cluster endpoint.")
	fs.StringVar(&o.KubeletClientTLSBootstrapToken, "kubelet-client-tls-bootstrap-token", env.WithDefaultString("KUBELET_CLIENT_TLS_BOOTSTRAP_TOKEN", ""), "[REQUIRED] The bootstrap token for new nodes to join the cluster.")
	fs.StringVar(&o.SSHPublicKey, "ssh-public-key", env.WithDefaultString("SSH_PUBLIC_KEY", ""), "[REQUIRED] VM SSH public key.")
	fs.StringVar(&o.NetworkPlugin, "network-plugin", env.WithDefaultString("NETWORK_PLUGIN", "azure"), "AKS cluster networking plugin.")
	fs.StringVar(&o.NetworkPolicy, "network-policy", env.WithDefaultString("NETWORK_POLICY", ""), "AKS cluster network policy.")
	fs.Var(newNodeIdentitiesValue(env.WithDefaultString("NODE_IDENTITIES", ""), &o.NodeIdentities), "node-identities", "User assigned identities for nodes.")
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

	// if ClusterID is not set, generate it from cluster endpoint
	// if clusterEndpoint is empty, it might awaiting merge from MergeSettings(), do not run GetAPIServerName() or it could panic
	// TODO: chore: remove clusterEndpoint validation logic here when karpenter-global-settings (and merge logic) are completely removed
	if o.ClusterID == "" && o.ClusterEndpoint != "" {
		o.ClusterID = getAKSClusterID(o.GetAPIServerName())
	}

	return nil
}

func (o *Options) ToContext(ctx context.Context) context.Context {
	return ToContext(ctx, o)
}

func (o *Options) MergeSettings(ctx context.Context) {
	s := settings.FromContext(ctx)
	mergeField(&o.ClusterName, s.ClusterName, o.setFlags["cluster-name"])
	mergeField(&o.ClusterEndpoint, s.ClusterEndpoint, o.setFlags["cluster-endpoint"])
	mergeField(&o.VMMemoryOverheadPercent, s.VMMemoryOverheadPercent, o.setFlags["vm-memory-overhead-percent"])
	mergeField(&o.ClusterID, s.ClusterID, o.setFlags["cluster-id"])
	mergeField(&o.KubeletClientTLSBootstrapToken, s.KubeletClientTLSBootstrapToken, o.setFlags["kubelet-client-tls-bootstrap-token"])
	mergeField(&o.SSHPublicKey, s.SSHPublicKey, o.setFlags["ssh-public-key"])
	mergeField(&o.NetworkPlugin, s.NetworkPlugin, o.setFlags["network-plugin"])
	mergeField(&o.NetworkPolicy, s.NetworkPolicy, o.setFlags["network-policy"])
	mergeField(&o.NodeIdentities, s.NodeIdentities, o.setFlags["node-identities"])

	if err := o.validateRequiredFields(); err != nil {
		panic(fmt.Errorf("checking required fields, %w", err))
	}

	// if ClusterID is not set, generate it from cluster endpoint
	if o.ClusterID == "" {
		o.ClusterID = getAKSClusterID(o.GetAPIServerName())
	}
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

func mergeField[T any](dest *T, src T, isDestSet bool) {
	if !isDestSet {
		*dest = src
	}
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
