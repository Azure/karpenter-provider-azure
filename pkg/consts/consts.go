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

package consts

const (
	NetworkPluginAzure = "azure"
	NetworkPluginNone  = "none"

	NetworkPluginModeOverlay = "overlay"
	NetworkPluginModeNone    = ""

	NetworkDataplaneNone   = ""
	NetworkDataplaneCilium = "cilium"
	NetworkDataplaneAzure  = "azure"

	StorageProfileManagedDisks = "ManagedDisks"
	StorageProfileEphemeral    = "Ephemeral"

	// All of these values for max pods match the aks defaults for max pods for the various
	// cni modes
	DefaultNetPluginNoneMaxPods = 250
	DefaultOverlayMaxPods       = 250
	DefaultNodeSubnetMaxPods    = 30
	DefaultKubernetesMaxPods    = 110

	ProvisionModeAKSScriptless       = "aksscriptless"
	ProvisionModeBootstrappingClient = "bootstrappingclient"
	ProvisionModeEKSHybrid           = "ekshybrid"
)
