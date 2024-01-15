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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/settings"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"
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
		"KUBELET_CLIENT_TLS_BOOTSTRAP_TOKEN",
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

		// Inject default settings
		var err error
		ctx, err = (&settings.Settings{}).Inject(ctx, nil)
		Expect(err).To(BeNil())
	})

	AfterEach(func() {
		for _, ev := range environmentVariables {
			os.Unsetenv(ev)
		}
		for ev, val := range envState {
			os.Setenv(ev, val)
		}
	})

	Context("Merging", func() {
		It("shouldn't overwrite options when all are set", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "options-cluster",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--vm-memory-overhead-percent", "0.1",
				"--cluster-id", "options-cluster-id",
				"--kubelet-client-tls-bootstrap-token", "options-bootstrap-token",
				"--ssh-public-key", "options-ssh-public-key",
				"--network-plugin", "azure",
				"--network-policy", "",
				"--node-identities", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/optionsid1,/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/optionsid2",
			)
			Expect(err).ToNot(HaveOccurred())
			ctx = settings.ToContext(ctx, &settings.Settings{
				ClusterName:                    "settings-cluster",
				ClusterEndpoint:                "https://karpenter-000000000001.hcp.westus2.staging.azmk8s.io",
				VMMemoryOverheadPercent:        0.1,
				ClusterID:                      "settings-cluster-id",
				KubeletClientTLSBootstrapToken: "settings-bootstrap-token",
				SSHPublicKey:                   "settings-ssh-public-key",
				NetworkPlugin:                  "kubenet",
				NetworkPolicy:                  "azure",
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid2"},
			})
			opts.MergeSettings(ctx)
			expectOptionsEqual(opts, test.Options(test.OptionsFields{
				ClusterName:                    lo.ToPtr("options-cluster"),
				ClusterEndpoint:                lo.ToPtr("https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io"),
				VMMemoryOverheadPercent:        lo.ToPtr(0.1),
				ClusterID:                      lo.ToPtr("options-cluster-id"),
				KubeletClientTLSBootstrapToken: lo.ToPtr("options-bootstrap-token"),
				SSHPublicKey:                   lo.ToPtr("options-ssh-public-key"),
				NetworkPlugin:                  lo.ToPtr("azure"),
				NetworkPolicy:                  lo.ToPtr(""),
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/optionsid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/optionsid2"},
			}))
		})
		It("should overwrite options when none are set", func() {
			err := opts.Parse(fs)
			Expect(err).ToNot(HaveOccurred())
			ctx = settings.ToContext(ctx, &settings.Settings{
				ClusterName:                    "settings-cluster",
				ClusterEndpoint:                "https://karpenter-000000000001.hcp.westus2.staging.azmk8s.io",
				VMMemoryOverheadPercent:        0.1,
				ClusterID:                      "settings-cluster-id",
				KubeletClientTLSBootstrapToken: "settings-bootstrap-token",
				SSHPublicKey:                   "settings-ssh-public-key",
				NetworkPlugin:                  "kubenet",
				NetworkPolicy:                  "azure",
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid2"},
			})
			opts.MergeSettings(ctx)
			expectOptionsEqual(opts, test.Options(test.OptionsFields{
				ClusterName:                    lo.ToPtr("settings-cluster"),
				ClusterEndpoint:                lo.ToPtr("https://karpenter-000000000001.hcp.westus2.staging.azmk8s.io"),
				VMMemoryOverheadPercent:        lo.ToPtr(0.1),
				ClusterID:                      lo.ToPtr("settings-cluster-id"),
				KubeletClientTLSBootstrapToken: lo.ToPtr("settings-bootstrap-token"),
				SSHPublicKey:                   lo.ToPtr("settings-ssh-public-key"),
				NetworkPlugin:                  lo.ToPtr("kubenet"),
				NetworkPolicy:                  lo.ToPtr("azure"),
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid2"},
			}))

		})
		It("should correctly merge options and settings when mixed", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "options-cluster",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--vm-memory-overhead-percent", "0.1",
				"--cluster-id", "options-cluster-id",
				"--kubelet-client-tls-bootstrap-token", "options-bootstrap-token",
			)
			Expect(err).ToNot(HaveOccurred())
			ctx = settings.ToContext(ctx, &settings.Settings{
				ClusterName:                    "settings-cluster",
				ClusterEndpoint:                "https://karpenter-000000000001.hcp.westus2.staging.azmk8s.io",
				VMMemoryOverheadPercent:        0.1,
				ClusterID:                      "settings-cluster-id",
				KubeletClientTLSBootstrapToken: "settings-bootstrap-token",
				SSHPublicKey:                   "settings-ssh-public-key",
				NetworkPlugin:                  "kubenet",
				NetworkPolicy:                  "azure",
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid2"},
			})
			opts.MergeSettings(ctx)
			expectOptionsEqual(opts, test.Options(test.OptionsFields{
				ClusterName:                    lo.ToPtr("options-cluster"),
				ClusterEndpoint:                lo.ToPtr("https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io"),
				VMMemoryOverheadPercent:        lo.ToPtr(0.1),
				ClusterID:                      lo.ToPtr("options-cluster-id"),
				KubeletClientTLSBootstrapToken: lo.ToPtr("options-bootstrap-token"),
				SSHPublicKey:                   lo.ToPtr("settings-ssh-public-key"),
				NetworkPlugin:                  lo.ToPtr("kubenet"),
				NetworkPolicy:                  lo.ToPtr("azure"),
				NodeIdentities:                 []string{"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid1", "/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/settingsid2"},
			}))
		})

		It("should correctly fallback to env vars when CLI flags aren't set", func() {
			os.Setenv("CLUSTER_NAME", "env-cluster")
			os.Setenv("CLUSTER_ENDPOINT", "https://env-cluster")
			os.Setenv("VM_MEMORY_OVERHEAD_PERCENT", "0.3")
			os.Setenv("CLUSTER_ID", "env-cluster-id")
			os.Setenv("KUBELET_CLIENT_TLS_BOOTSTRAP_TOKEN", "env-bootstrap-token")
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
		It("should fail validation with panic when clusterName not included", func() {
			err := opts.Parse(fs, "--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io")
			//Expect(err).To(HaveOccurred()) // TODO: Add back when karpenter-global-settings (and merge logic) are completely removed

			// TODO: Remove below when karpenter-global-settings (and merge logic) are completely removed
			Expect(err).ToNot(HaveOccurred())
			ctx = settings.ToContext(ctx, &settings.Settings{})
			Expect(func() { opts.MergeSettings(ctx) }).To(Panic())

		})
		It("should fail validation with panic when clusterEndpoint not included", func() {
			err := opts.Parse(fs, "--cluster-name", "my-name")
			//Expect(err).To(HaveOccurred()) // TODO: Add back when karpenter-global-settings (and merge logic) are completely removed

			// TODO: Remove below when karpenter-global-settings (and merge logic) are completely removed
			Expect(err).ToNot(HaveOccurred())
			ctx = settings.ToContext(ctx, &settings.Settings{})
			Expect(func() { opts.MergeSettings(ctx) }).To(Panic())
		})
		It("should fail when clusterEndpoint is invalid (not absolute)", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
			)
			Expect(err).To(HaveOccurred())
		})
		It("should fail when vmMemoryOverheadPercent is negative", func() {
			err := opts.Parse(
				fs,
				"--cluster-name", "my-name",
				"--cluster-endpoint", "https://karpenter-000000000000.hcp.westus2.staging.azmk8s.io",
				"--vm-memory-overhead-percent", "-0.01",
			)
			Expect(err).To(HaveOccurred())
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
