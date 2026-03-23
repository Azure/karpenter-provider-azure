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
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func (o *Options) IsAzureCNIOverlay() bool {
	return o.NetworkPlugin == consts.NetworkPluginAzure && o.NetworkPluginMode == consts.NetworkPluginModeOverlay
}

func (o *Options) IsCiliumNodeSubnet() bool {
	return o.NetworkPlugin == consts.NetworkPluginAzure && o.NetworkPluginMode == consts.NetworkPluginModeNone && o.NetworkDataplane == consts.NetworkDataplaneCilium
}

func (o *Options) IsNetworkPluginNone() bool {
	return o.NetworkPlugin == consts.NetworkPluginNone
}

// IsAzureVMMode returns true if the provision mode is AzureVM (non-AKS generic Azure VM provisioning).
// Prefer checking NodeClaim's NodeClassRef.Kind over this where possible, to support
// mixed-mode clusters. This helper is only for code paths that don't have a NodeClaim
// (e.g., controller registration, validation).
func (o *Options) IsAzureVMMode() bool {
	return o.ProvisionMode == consts.ProvisionModeAzureVM
}
