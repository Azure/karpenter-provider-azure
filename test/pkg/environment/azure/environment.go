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

package azure

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/zone"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/test/azure"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func init() {
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": v1.LabelTopologyZone})
	coretest.DefaultImage = "mcr.microsoft.com/oss/kubernetes/pause:3.6"
}

const (
	CiliumAgentNotReadyTaint    = "node.cilium.io/agent-not-ready"
	EphemeralInitContainerImage = "alpine"

	ciliumStartupTaintTolerationSeconds int64 = 120
)

type Environment struct {
	*common.Environment

	NodeResourceGroup    string
	Region               string
	SubscriptionID       string
	VNETResourceGroup    string
	ACRName              string
	ClusterName          string
	MachineAgentPoolName string
	ClusterResourceGroup string
	CloudConfig          cloud.Configuration
	ProvisionMode        string

	tracker *azure.Tracker

	// These should be unexported and access should be through the Environment methods
	// Any create calls should make sure they also register the created resources with the Environment's tracker
	// to ensure they are cleaned up after the test.
	vmClient             *armcompute.VirtualMachinesClient
	vnetClient           *armnetwork.VirtualNetworksClient
	subnetClient         *armnetwork.SubnetsClient
	interfacesClient     *armnetwork.InterfacesClient
	managedClusterClient *containerservice.ManagedClustersClient
	agentPoolClient      *containerservice.AgentPoolsClient
	machinesClient       *containerservice.MachinesClient
	zoneProvider         *zone.Provider

	// Public Clients
	KeyVaultClient          *armkeyvault.VaultsClient
	DiskEncryptionSetClient *armcompute.DiskEncryptionSetsClient

	defaultCredential azcore.TokenCredential

	RBACManager *RBACManager
}

func readEnvRequired(name string) string {
	value, exists := os.LookupEnv(name)
	if !exists {
		panic(fmt.Sprintf("Environment variable %s is not set", name))
	}
	if value == "" {
		panic(fmt.Sprintf("Environment variable %s is set to an empty string", name))
	}
	return value
}

func readEnvOptional(name string) string {
	value, exists := os.LookupEnv(name)
	if !exists {
		return ""
	}
	return value
}

func getCloudEnvironment() *auth.Environment {
	cfg := auth.Config{}
	lo.Must0(cfg.Build(), "Failed to build cloud environment")
	lo.Must0(cfg.Default(), "Failed to set default cloud environment")
	// This is a hack so we can re-use the same validate, even though in this test context we don't need a real subscription ID
	cfg.SubscriptionID = "1234"
	lo.Must0(cfg.Validate(), "Failed to validate cloud environment")

	env, err := auth.ResolveCloudEnvironment(&cfg)
	lo.Must0(err, "Failed to resolve cloud environment")
	return env
}

func NewEnvironment(t *testing.T) *Environment {
	cloudEnv := getCloudEnvironment()

	azureEnv := &Environment{
		Environment:          common.NewEnvironment(t),
		SubscriptionID:       readEnvRequired("AZURE_SUBSCRIPTION_ID"),
		ClusterName:          readEnvRequired("AZURE_CLUSTER_NAME"),
		ClusterResourceGroup: readEnvRequired("AZURE_RESOURCE_GROUP"),
		ACRName:              readEnvRequired("AZURE_ACR_NAME"),
		ProvisionMode:        readEnvOptional("PROVISION_MODE"),
		Region:               lo.Ternary(os.Getenv("AZURE_LOCATION") == "", "westus2", os.Getenv("AZURE_LOCATION")),
		CloudConfig:          cloudEnv.Cloud,
		tracker:              azure.NewTracker(),
	}

	defaultNodeRG := fmt.Sprintf("MC_%s_%s_%s", azureEnv.ClusterResourceGroup, azureEnv.ClusterName, azureEnv.Region)
	azureEnv.VNETResourceGroup = lo.Ternary(os.Getenv("VNET_RESOURCE_GROUP") == "", defaultNodeRG, os.Getenv("VNET_RESOURCE_GROUP"))
	azureEnv.NodeResourceGroup = defaultNodeRG

	credOptions := &azidentity.DefaultAzureCredentialOptions{
		ClientOptions: policy.ClientOptions{
			Cloud: cloudEnv.Cloud,
		},
		TenantID: os.Getenv("AZURE_TENANT_ID"),
	}
	cred := lo.Must(azidentity.NewDefaultAzureCredential(credOptions))
	azureEnv.defaultCredential = cred

	clientOptions := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Cloud: azureEnv.CloudConfig,
		},
	}
	byokRetryOptions := azureEnv.ClientOptionsForRBACPropagation()
	azureEnv.vmClient = lo.Must(armcompute.NewVirtualMachinesClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.vnetClient = lo.Must(armnetwork.NewVirtualNetworksClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.subnetClient = lo.Must(armnetwork.NewSubnetsClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.interfacesClient = lo.Must(armnetwork.NewInterfacesClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.managedClusterClient = lo.Must(containerservice.NewManagedClustersClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.agentPoolClient = lo.Must(containerservice.NewAgentPoolsClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.machinesClient = lo.Must(containerservice.NewMachinesClient(azureEnv.SubscriptionID, cred, clientOptions))
	azureEnv.KeyVaultClient = lo.Must(armkeyvault.NewVaultsClient(azureEnv.SubscriptionID, cred, byokRetryOptions))
	azureEnv.DiskEncryptionSetClient = lo.Must(armcompute.NewDiskEncryptionSetsClient(azureEnv.SubscriptionID, cred, byokRetryOptions))
	azureEnv.RBACManager = lo.Must(NewRBACManager(azureEnv.SubscriptionID, cred))
	subscriptionsClient := lo.Must(armsubscriptions.NewClient(cred, nil))
	azureEnv.zoneProvider = zone.NewProvider(subscriptionsClient, realClock{}, azureEnv.SubscriptionID)
	// If ProvisionMode wasn't set, default to scriptless, though note that this is
	// actually defaulted dynamically based on the value of a toggle in AKS which means
	// assuming we're always in ProvisionMode Scriptless here is incorrect at times, though OK
	// for our current usage.
	if azureEnv.ProvisionMode == "" {
		azureEnv.ProvisionMode = consts.ProvisionModeAKSScriptless
	}
	// Default to reserved managed machine agentpool name for NAP
	azureEnv.MachineAgentPoolName = "aksmanagedap"
	if azureEnv.InClusterController {
		azureEnv.MachineAgentPoolName = "testmpool"
	}
	// Confirm we have a machine pool
	if azureEnv.InClusterController && azureEnv.IsAKSMachineAPIMode() {
		azureEnv.ExpectMachinesAgentPoolExists()
	}
	return azureEnv
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (env *Environment) GetDefaultCredential() azcore.TokenCredential {
	return env.defaultCredential
}

// SupportsZones returns true if the region supports availability zones
func (env *Environment) SupportsZones() bool {
	return env.zoneProvider.SupportsZones(context.Background(), env.Region)
}

// GetAvailableZones returns the list of available zones for the current region.
// Returns nil if the region doesn't support zones.
func (env *Environment) GetAvailableZones() []string {
	return env.zoneProvider.GetAvailableZones(context.Background(), env.Region)
}

// Retry options for BYOK-related clients that may encounter RBAC propagation delays
// RBAC assignments can take time to propagate, resulting in 403 Forbidden errors
// With 15 retries at 5 second intervals = 75 seconds total retry time
func (env *Environment) ClientOptionsForRBACPropagation() *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Cloud: env.CloudConfig,
			Retry: policy.RetryOptions{
				MaxRetries: 15,
				RetryDelay: time.Second * 5,
				StatusCodes: []int{
					http.StatusForbidden, // RBAC assignments haven't propagated yet
				},
			},
		},
	}
}

func (env *Environment) IsAKSMachineAPIMode() bool {
	return env.ProvisionMode == consts.ProvisionModeAKSMachineAPI || env.ProvisionMode == consts.ProvisionModeAKSMachineAPIHeaderBatch
}

func (env *Environment) IsMachineModeOrNPS() bool {
	// Assumption is if we're not in the cluster, we're in NPS mode. Ideally we would just check this via ProvisionMode, but
	// we can't do that right now as depending on context we may not set provision mode for the tests
	return env.IsAKSMachineAPIMode() || !env.InClusterController
}

func (env *Environment) UsesSharedImageGallery() bool {
	return env.IsMachineModeOrNPS()
}

func (env *Environment) DefaultAKSNodeClass() *v1beta1.AKSNodeClass {
	nodeClass := test.AKSNodeClass()
	return nodeClass
}

func (env *Environment) AZLinuxNodeClass() *v1beta1.AKSNodeClass {
	nodeClass := env.DefaultAKSNodeClass()
	nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
	return nodeClass
}

// Pod wraps coretest.Pod for Azure E2E tests; use it instead of coretest.Pod when the test should apply Azure environment defaults.
// Currently this is any time one has to work around taint race described in https://github.com/Azure/karpenter-provider-azure/issues/1625
// and cannot use Deployment instead.
func (env *Environment) Pod(options coretest.PodOptions) *v1.Pod {
	// Keep pod-based tests resilient to the Cilium startup-taint race while bounding how long the pod can tolerate it.
	if env.IsCilium() {
		options.Tolerations = append(options.Tolerations, v1.Toleration{
			Key:               CiliumAgentNotReadyTaint,
			Operator:          v1.TolerationOpExists,
			Effect:            v1.TaintEffectNoExecute,
			TolerationSeconds: new(ciliumStartupTaintTolerationSeconds),
		})
	}
	return coretest.Pod(options)
}
