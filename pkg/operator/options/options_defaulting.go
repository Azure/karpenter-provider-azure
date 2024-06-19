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
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	armopts "github.com/Azure/karpenter-provider-azure/pkg/utils/opts"
)

// Default sets the default values for the options, but only for those too complicated to be in env default (e.g., depends on other envs)
func (o *Options) Default(ctx context.Context) error {
	var err error

	var authManager *auth.AuthManager
	if o.ArmAuthMethod == auth.AuthMethodWorkloadIdentity {
		authManager = auth.NewAuthManagerWorkloadIdentity(o.Location)
	} else if o.ArmAuthMethod == auth.AuthMethodSysMSI {
		authManager = auth.NewAuthManagerSystemAssignedMSI(o.Location)
	}

	if o.APIServerName, err = getAPIServerName(o.ClusterEndpoint); err != nil {
		return fmt.Errorf("failed to get APIServerName: %w", err)
	}

	if o.ClusterID, err = getAKSClusterID(o.APIServerName); err != nil {
		return fmt.Errorf("failed to get ClusterID: %w", err)
	}

	if o.VnetGUID, err = getVnetGUID(ctx, o.SubscriptionID, o.SubnetID, authManager); err != nil {
		return fmt.Errorf("failed to get VnetGUID: %w", err)

	}

	return nil
}

func getAPIServerName(clusterEndpoint string) (string, error) {
	endpoint, err := url.Parse(clusterEndpoint) // assume to already validated
	return endpoint.Hostname(), err
}

// getAKSClusterID returns cluster ID based on the DNS prefix of the cluster.
// The logic comes from AgentBaker and other places, originally from aks-engine
// with the additional assumption of DNS prefix being the first 33 chars of FQDN
func getAKSClusterID(apiServerFQDN string) (string, error) {
	dnsPrefix := apiServerFQDN[:33]
	h := fnv.New64a()
	h.Write([]byte(dnsPrefix))
	r := rand.New(rand.NewSource(int64(h.Sum64()))) //nolint:gosec
	return fmt.Sprintf("%08d", r.Uint32())[:8], nil
}

func getVnetGUID(ctx context.Context, subscriptionID string, VnetSubnetID string, authManager *auth.AuthManager) (string, error) {
	creds, err := authManager.NewCredential()
	if err != nil {
		return "", err
	}
	armOpts := armopts.DefaultArmOpts()
	vnetClient, err := armnetwork.NewVirtualNetworksClient(subscriptionID, creds, armOpts)
	if err != nil {
		return "", err
	}

	subnetParts, err := utils.GetVnetSubnetIDComponents(VnetSubnetID)
	if err != nil {
		return "", err
	}
	vnet, err := vnetClient.Get(ctx, subnetParts.ResourceGroupName, subnetParts.VNetName, nil)
	if err != nil {
		return "", err
	}
	if vnet.Properties == nil || vnet.Properties.ResourceGUID == nil {
		return "", fmt.Errorf("vnet %s does not have a resource GUID", subnetParts.VNetName)
	}
	return *vnet.Properties.ResourceGUID, nil
}

func contains(slice []string, target string) bool {
	for _, element := range slice {
		if target == element {
			return true
		}
	}
	return false
}
