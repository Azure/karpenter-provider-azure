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

package bootstrap

// cloud-init destination file references.
const (
	cseHelpersScriptFilepath             = "/opt/azure/containers/provision_source.sh"
	cseHelpersScriptDistroFilepath       = "/opt/azure/containers/provision_source_distro.sh"
	cseInstallScriptFilepath             = "/opt/azure/containers/provision_installs.sh"
	cseInstallScriptDistroFilepath       = "/opt/azure/containers/provision_installs_distro.sh"
	cseConfigScriptFilepath              = "/opt/azure/containers/provision_configs.sh"
	customSearchDomainsCSEScriptFilepath = "/opt/azure/containers/setup-custom-search-domains.sh"
	dhcpV6ServiceCSEScriptFilepath       = "/etc/systemd/system/dhcpv6.service"
	dhcpV6ConfigCSEScriptFilepath        = "/opt/azure/containers/enable-dhcpv6.sh"
	initAKSCustomCloudFilepath           = "/opt/azure/containers/init-aks-custom-cloud.sh"
)

const (
	standard           = "standard"
	vmss               = "vmss"
	azure              = "azure"
	kubenet            = "kubenet"
	calico             = "calico"
	lbBasic            = "basic"
	lbStandard         = "Standard"
	vmSizeStandardDc2s = "Standard_DC2s"
	vmSizeStandardDc4s = "Standard_DC4s"
	defaultLinuxUser   = "azureuser"
	defaultCloudName   = "AzurePublicCloud"
)
