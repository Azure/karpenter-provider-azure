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

// All the helper functions should be hosted by another public repo later. (e.g. agentbaker)
// Helper functions in this file will be called by bootstrappers to populate nb contract payload.

package bootstrap

import (
	_ "embed"
	"encoding/base64"
	"strings"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
	"knative.dev/pkg/ptr"
)

// getIdentityConfig returns the identityConfig object based on the identity inputs.
func getIdentityConfig(servicePrincipalID string, servicePrincipalSecret string, userAssignedIdentityID string) *nbcontractv1.IdentityConfig {
	identityConfig := nbcontractv1.IdentityConfig{
		IdentityType:                nbcontractv1.IdentityType_IDENTITY_TYPE_UNSPECIFIED,
		ServicePrincipalId:          ptr.String(""),
		ServicePrincipalSecret:      ptr.String(""),
		AssignedIdentityId:          ptr.String(""),
		UseManagedIdentityExtension: ptr.String("false"),
	}

	if userAssignedIdentityID != "" {
		identityConfig.IdentityType = nbcontractv1.IdentityType_IDENTITY_TYPE_USER_IDENTITY
		*identityConfig.AssignedIdentityId = userAssignedIdentityID
		return &identityConfig
	}

	if (servicePrincipalID != "" || servicePrincipalID == "msi") && (servicePrincipalSecret != "" || servicePrincipalSecret == base64.StdEncoding.EncodeToString([]byte("msi"))) {
		identityConfig.IdentityType = nbcontractv1.IdentityType_IDENTITY_TYPE_SERVICE_PRINCIPAL
		*identityConfig.ServicePrincipalId = servicePrincipalID
		*identityConfig.ServicePrincipalSecret = servicePrincipalSecret
		return &identityConfig
	}

	return &identityConfig
}

// getLoadBalancerSKI returns the LoadBalancerSku enum based on the input string.
func getLoadBalancerSKU(sku string) nbcontractv1.LoadBalancerSku {
	if strings.EqualFold(sku, "Standard") {
		return nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_STANDARD
	} else if strings.EqualFold(sku, "Basic") {
		return nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_BASIC
	}

	return nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_UNSPECIFIED
}

// getNetworkModeType returns the NetworkMode enum based on the input string.
func getNetworkModeType(networkMode string) nbcontractv1.NetworkModeType {
	if strings.EqualFold(networkMode, "transparent") {
		return nbcontractv1.NetworkModeType_NETWORK_MODE_TRANSPARENT
	} else if strings.EqualFold(networkMode, "l2bridge") {
		return nbcontractv1.NetworkModeType_NETWORK_MODE_L2BRIDGE
	}

	return nbcontractv1.NetworkModeType_NETWORK_MODE_UNSPECIFIED
}

// getNetworkPluginType returns the NetworkPluginType enum based on the input string.
func getNetworkPluginType(networkPlugin string) nbcontractv1.NetworkPluginType {
	if strings.EqualFold(networkPlugin, "azure") {
		return nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_AZURE
	} else if strings.EqualFold(networkPlugin, "kubenet") {
		return nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_KUBENET
	}

	return nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_NONE
}

// getNetworkPolicyType returns the NetworkPolicyType enum based on the input string.
func getNetworkPolicyType(networkPolicy string) nbcontractv1.NetworkPolicyType {
	if strings.EqualFold(networkPolicy, "azure") {
		return nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_AZURE
	} else if strings.EqualFold(networkPolicy, "calico") {
		return nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_CALICO
	}

	return nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_NONE
}
