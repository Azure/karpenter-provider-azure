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
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func (o *Options) IsAzureCNIOverlay() bool {
	return o.NetworkPlugin == consts.NetworkPluginAzure && o.NetworkPluginMode == consts.NetworkPluginModeOverlay
}

// IsIPv6Enabled reports whether the cluster is dual-stack (its IP families include IPv6),
// in which case provisioned nodes must be configured with an IPv6 NIC IP configuration.
// Mirrors the AKS RP's IsIPv6Enabled signal, which is derived from networkProfile.ipFamilies.
func (o *Options) IsIPv6Enabled() bool {
	for _, family := range o.NodeIPFamilies {
		if strings.EqualFold(family, "IPv6") {
			return true
		}
	}
	return false
}

func (o *Options) IsCiliumNodeSubnet() bool {
	return o.NetworkPlugin == consts.NetworkPluginAzure && o.NetworkPluginMode == consts.NetworkPluginModeNone && o.NetworkDataplane == consts.NetworkDataplaneCilium
}

func (o *Options) IsNetworkPluginNone() bool {
	return o.NetworkPlugin == consts.NetworkPluginNone
}
