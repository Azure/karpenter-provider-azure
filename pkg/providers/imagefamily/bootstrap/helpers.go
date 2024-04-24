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
func getLoadBalancerSKU(sku string) nbcontractv1.LoadBalancerConfig_LoadBalancerSku {
	if strings.EqualFold(sku, "Standard") {
		return nbcontractv1.LoadBalancerConfig_STANDARD
	} else if strings.EqualFold(sku, "Basic") {
		return nbcontractv1.LoadBalancerConfig_BASIC
	}

	return nbcontractv1.LoadBalancerConfig_UNSPECIFIED
}

// getNetworkPluginType returns the NetworkPluginType enum based on the input string.
func getNetworkPluginType(networkPlugin string) nbcontractv1.NetworkPlugin {
	if strings.EqualFold(networkPlugin, "azure") {
		return nbcontractv1.NetworkPlugin_NP_AZURE
	} else if strings.EqualFold(networkPlugin, "kubenet") {
		return nbcontractv1.NetworkPlugin_NP_KUBENET
	}

	return nbcontractv1.NetworkPlugin_NP_NONE
}

// getNetworkPolicyType returns the NetworkPolicyType enum based on the input string.
func getNetworkPolicyType(networkPolicy string) nbcontractv1.NetworkPolicy {
	if strings.EqualFold(networkPolicy, "azure") {
		return nbcontractv1.NetworkPolicy_NPO_AZURE
	} else if strings.EqualFold(networkPolicy, "calico") {
		return nbcontractv1.NetworkPolicy_NPO_CALICO
	}

	return nbcontractv1.NetworkPolicy_NPO_NONE
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
