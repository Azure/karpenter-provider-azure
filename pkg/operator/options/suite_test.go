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
			os.Setenv("CLUSTER_ENDPOINT", "https://env-cluster")
			os.Setenv("VM_MEMORY_OVERHEAD_PERCENT", "0.3")
			os.Setenv("CLUSTER_ID", "env-cluster-id")
			os.Setenv("KUBELET_BOOTSTRAP_TOKEN", "env-bootstrap-token")
			os.Setenv("SSH_PUBLIC_KEY", "env-ssh-public-key")
			os.Setenv("NETWORK_PLUGIN", "env-network-plugin")
			os.Setenv("NETWORK_POLICY", "env-network-policy")
			os.Setenv("NODE_IDENTITIES", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1,/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2")
			fs = &coreoptions.FlagSet{
				FlagSet: flag.NewFlagSet("karpenter", flag.ContinueOnError),
			}
			opts.AddFlags(fs)
			err := opts.Parse(fs)
			Expect(err).ToNot(HaveOccurred())
			expectOptionsEqual(opts, test.Options(test.OptionsFields{
				ClusterName:                    lo.ToPtr("env-cluster"),
				ClusterEndpoint:                lo.ToPtr("https://env-cluster"),
				VMMemoryOverheadPercent:        lo.ToPtr(0.3),
				ClusterID:                      lo.ToPtr("env-cluster-id"),
				KubeletClientTLSBootstrapToken: lo.ToPtr("env-bootstrap-token"),
				SSHPublicKey:                   lo.ToPtr("env-ssh-public-key"),
				NetworkPlugin:                  lo.ToPtr("env-network-plugin"),
				NetworkPolicy:                  lo.ToPtr("env-network-policy"),
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/envid2"},
			}))
		})
	})

	Context("Validation", func() {
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
