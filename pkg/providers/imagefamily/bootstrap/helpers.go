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
	"strings"

	"github.com/Azure/agentbaker/pkg/agent/datamodel"
	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
	"github.com/blang/semver"
)

const (
	azureChinaCloud = "AzureChinaCloud"
)

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

// getFeatureState takes a positive enablement state variable as input. For a negative case, please invert it (from true to false or vice versa) before passing in.
// For example, variable XXX_enabled is a correct input while XXX_disabled is incorrect.
func getFeatureState(enabled bool) nbcontractv1.FeatureState {
	if enabled {
		return nbcontractv1.FeatureState_FEATURE_STATE_ENABLED
	} else if !enabled {
		return nbcontractv1.FeatureState_FEATURE_STATE_DISABLED
	}

	return nbcontractv1.FeatureState_FEATURE_STATE_UNSPECIFIED
}

// GetOutBoundCmd returns a proper outbound traffic command based on some cloud and Linux distro configs.
func GetOutBoundCmd(nbconfig *datamodel.NodeBootstrappingConfiguration, cloudName string) string {
	cs := nbconfig.ContainerService
	if cs.Properties.FeatureFlags.IsFeatureEnabled("BlockOutboundInternet") {
		return ""
	}

	registry := ""
	switch {
	case cloudName == azureChinaCloud:
		registry = `gcr.azk8s.cn`
	case cs.IsAKSCustomCloud():
		registry = cs.Properties.CustomCloudEnv.McrURL
	default:
		registry = `mcr.microsoft.com`
	}

	if registry == "" {
		return ""
	}

	// curl on Ubuntu 16.04 (shipped prior to AKS 1.18) doesn't support proxy TLS.
	// so we need to use nc for the connectivity check.
	clusterVersion, _ := semver.Make(cs.Properties.OrchestratorProfile.OrchestratorVersion)
	minVersion, _ := semver.Make("1.18.0")

	var connectivityCheckCommand string
	if clusterVersion.GTE(minVersion) {
		connectivityCheckCommand = `curl -v --insecure --proxy-insecure https://` + registry + `/v2/`
	} else {
		connectivityCheckCommand = `nc -vz ` + registry + ` 443`
	}

	return connectivityCheckCommand
}

// GetDefaultOutboundCommand returns a default outbound traffic command.
func GetDefaultOutboundCommand() string {
	return "curl -v --insecure --proxy-insecure https://mcr.microsoft.com/v2/"
}
