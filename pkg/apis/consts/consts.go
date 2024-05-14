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
	NetworkPluginAzure   = "azure"
	NetworkPluginKubenet = "kubenet"

	PodNetworkTypeOverlay = "overlay"
	PodNetworkTypeNone    = ""

	NetworkDataplaneCilium = "cilium"

	// The general idea here is we don't need to allocate secondary ips for host network pods
	// If you bring a podsubnet this happens automatically but in static azure cni we know that
	// kube-proxy and ip-masq-agent are both host network and thus don't need an ip.
	StaticAzureCNIHostNetworkAddons = 2
)
