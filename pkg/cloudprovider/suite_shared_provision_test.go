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

package cloudprovider

// suite_shared_provision_test.go provides mode-agnostic setup functions and helpers
// for the test reunification framework. Each provision mode (AKSScriptless, AKSMachineAPIHeaderBatch)
// has a setup function that configures the shared test variables (azureEnv, cloudProvider,
// cluster, coreProvisioner, etc.) so that the same test logic can run under any mode.
//
// Usage pattern in test suites:
//
//   Context("ProvisionMode = AKSMachineAPIHeaderBatch", func() {
//       BeforeEach(func() { setupAKSMachineAPIMode() })
//       AfterEach(func() { teardownMode() })
//       // shared tests here
//   })
//   Context("ProvisionMode = AKSScriptless", func() {
//       BeforeEach(func() { setupAKSScriptlessMode() })
//       AfterEach(func() { teardownMode() })
//       // same shared tests here
//   })

import (
	"github.com/samber/lo"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

// setupAKSMachineAPIMode configures the test environment for AKS Machine API (header batch) provision mode.
// This sets up: testOptions, ctx, azureEnv, azureEnvNonZonal, statusController, cloudProvider,
// cloudProviderNonZonal, cluster, clusterNonZonal, coreProvisioner, coreProvisionerNonZonal.
func setupAKSMachineAPIMode() {
	testOptions = test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPIHeaderBatch),
		UseSIG:        lo.ToPtr(true),
	})

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, testOptions)

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
	test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)

	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

	ExpectApplied(ctx, env.Client, nodeClass, nodePool)
	ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
}

// setupAKSScriptlessMode configures the test environment for AKS Scriptless (VM) provision mode.
// This sets up: testOptions, ctx, azureEnv, azureEnvNonZonal, cloudProvider, cloudProviderNonZonal,
// cluster, clusterNonZonal, coreProvisioner, coreProvisionerNonZonal.
func setupAKSScriptlessMode() {
	testOptions = test.Options(test.OptionsFields{
		ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSScriptless),
	})
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, testOptions)

	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
	cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)

	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)
}

// teardownAKSMachineAPIMode cleans up after AKS Machine API mode tests.
func teardownAKSMachineAPIMode() {
	cloudProvider.WaitForInstancePromises()
	cluster.Reset()
	azureEnv.Reset()
	azureEnvNonZonal.Reset()
}

// teardownAKSScriptlessMode cleans up after AKS Scriptless mode tests.
func teardownAKSScriptlessMode() {
	cloudProvider.WaitForInstancePromises()
	cluster.Reset()
	azureEnv.Reset()
	azureEnvNonZonal.Reset()
}
