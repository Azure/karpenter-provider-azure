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

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"text/template"
)

const (
	globalAKSMirror = "https://acs-mirror.azureedge.net"
)

// NOTE: embed only works on vars not defined in a function, so without putting this into an internal package for encapulation, we are stuck with these remaining vars.
var (
	//go:embed cse_cmd.sh.gtpl
	customDataTemplateText string

	//go:embed  containerd.toml.gtpl
	containerdConfigTemplateText string

	//go:embed sysctl.conf
	sysctlContent []byte
)

func getCustomDataTemplate() *template.Template {
	return template.Must(template.New("customdata").Parse(customDataTemplateText))
}

func getContainerdConfigTemplate() *template.Template {
	return template.Must(template.New("containerdconfig").Parse(containerdConfigTemplateText))
}

func getBaseKubeletFlags() map[string]string {
	// source note: unique per nodepool. partially user-specified, static, and RP-generated
	// removed --image-pull-progress-deadline=30m  (not in 1.24?)
	// removed --network-plugin=cni (not in 1.24?)
	// removed --azure-container-registry-config (not in 1.30)
	// removed --keep-terminated-pod-volumes (not in 1.31)
	return map[string]string{
		"--address":                           "0.0.0.0",
		"--anonymous-auth":                    "false",
		"--authentication-token-webhook":      "true",
		"--authorization-mode":                "Webhook",
		"--cgroups-per-qos":                   "true",
		"--client-ca-file":                    "/etc/kubernetes/certs/ca.crt",
		"--cloud-config":                      "/etc/kubernetes/azure.json",
		"--cloud-provider":                    "external",
		"--cluster-dns":                       "10.0.0.10",
		"--cluster-domain":                    "cluster.local",
		"--enforce-node-allocatable":          "pods",
		"--event-qps":                         "0",
		"--eviction-hard":                     "memory.available<750Mi,nodefs.available<10%,nodefs.inodesFree<5%",
		"--image-gc-high-threshold":           "85",
		"--image-gc-low-threshold":            "80",
		"--kubeconfig":                        "/var/lib/kubelet/kubeconfig",
		"--max-pods":                          "110",
		"--node-status-update-frequency":      "10s",
		"--pod-infra-container-image":         "mcr.microsoft.com/oss/kubernetes/pause:3.6",
		"--pod-manifest-path":                 "/etc/kubernetes/manifests",
		"--pod-max-pids":                      "-1",
		"--protect-kernel-defaults":           "true",
		"--read-only-port":                    "0",
		"--resolv-conf":                       "/run/systemd/resolve/resolv.conf",
		"--rotate-certificates":               "true",
		"--streaming-connection-idle-timeout": "4h",
		"--tls-cert-file":                     "/etc/kubernetes/certs/kubeletserver.crt",
		"--tls-cipher-suites":                 "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_128_GCM_SHA256",
		"--tls-private-key-file":              "/etc/kubernetes/certs/kubeletserver.key",
	}
}

func getStaticNodeBootstrapVars() *NodeBootstrapVariables {
	vnetCNILinuxPluginsURL := fmt.Sprintf("%s/azure-cni/v1.4.32/binaries/azure-vnet-cni-linux-amd64-v1.4.32.tgz", globalAKSMirror)
	cniPluginsURL := fmt.Sprintf("%s/cni-plugins/v1.1.1/binaries/cni-plugins-linux-amd64-v1.1.1.tgz", globalAKSMirror)

	// baseline, covering unused (-), static (s), and unsupported (n) fields,
	// as well as defaults, cluster/node level (cd/td/xd)
	return &NodeBootstrapVariables{
		IsAKSCustomCloud:                  false,                  // n
		InitAKSCustomCloudFilepath:        "",                     // n
		AKSCustomCloudRepoDepotEndpoint:   "",                     // n
		AdminUsername:                     "azureuser",            // td
		MobyVersion:                       "",                     // -
		HyperkubeURL:                      "",                     // -
		KubeBinaryURL:                     "",                     // cd
		CustomKubeBinaryURL:               "",                     // -
		KubeproxyURL:                      "",                     // -
		VMType:                            "vmss",                 // xd
		Subnet:                            "aks-subnet",           // xd
		VirtualNetworkResourceGroup:       "",                     // xd
		PrimaryAvailabilitySet:            "",                     // -
		PrimaryScaleSet:                   "",                     // -
		ServicePrincipalClientID:          "msi",                  // ad
		VNETCNILinuxPluginsURL:            vnetCNILinuxPluginsURL, // - [currently required, installCNI in provisioning scripts depends on CNI_PLUGINS_URL]
		CNIPluginsURL:                     cniPluginsURL,          // - [currently required, same]
		CloudProviderBackoff:              true,                   // s
		CloudProviderBackoffMode:          "v2",                   // s
		CloudProviderBackoffRetries:       "6",                    // s
		CloudProviderBackoffExponent:      "0",                    // s
		CloudProviderBackoffDuration:      "5",                    // s
		CloudProviderBackoffJitter:        "0",                    // s
		CloudProviderRatelimit:            true,                   // s
		CloudProviderRatelimitQPS:         "10",                   // s
		CloudProviderRatelimitQPSWrite:    "10",                   // s
		CloudProviderRatelimitBucket:      "100",                  // s
		CloudProviderRatelimitBucketWrite: "100",                  // s
		LoadBalancerDisableOutboundSNAT:   false,                  // xd
		UseManagedIdentityExtension:       true,                   // s
		UseInstanceMetadata:               true,                   // s
		LoadBalancerSKU:                   "Standard",             // xd
		ExcludeMasterFromStandardLB:       true,                   // s
		MaximumLoadbalancerRuleCount:      250,                    // xd
		ContainerRuntime:                  "containerd",           // s
		CLITool:                           "ctr",                  // s
		ContainerdDownloadURLBase:         "",                     // -
		NetworkMode:                       "",                     // cd
		IsVHD:                             true,                   // s
		SGXNode:                           false,                  // -
		MIGNode:                           false,                  // td
		ConfigGPUDriverIfNeeded:           true,                   // s
		EnableGPUDevicePluginIfNeeded:     false,                  // -
		TeleportdPluginDownloadURL:        "",                     // -
		ContainerdVersion:                 "",                     // -
		ContainerdPackageURL:              "",                     // -
		RuncVersion:                       "",                     // -
		RuncPackageURL:                    "",                     // -
		DisableSSH:                        false,                  // td
		EnableHostsConfigAgent:            false,                  // n
		NeedsContainerd:                   true,                   // s
		TeleportEnabled:                   false,                  // td
		ShouldConfigureHTTPProxy:          false,                  // cd
		ShouldConfigureHTTPProxyCA:        false,                  // cd
		HTTPProxyTrustedCA:                "",                     // cd
		ShouldConfigureCustomCATrust:      false,                  // cd
		CustomCATrustConfigCerts:          []string{},             // cd

		OutboundCommand:                         "curl -v --insecure --proxy-insecure https://mcr.microsoft.com/v2/", // s
		EnableUnattendedUpgrades:                false,                                                               // cd
		IsKrustlet:                              false,                                                               // td
		ShouldConfigSwapFile:                    false,                                                               // td
		ShouldConfigTransparentHugePage:         false,                                                               // td
		TargetCloud:                             "AzurePublicCloud",                                                  // n
		TargetEnvironment:                       "AzurePublicCloud",                                                  // n
		CustomEnvJSON:                           "",                                                                  // n
		IsCustomCloud:                           false,                                                               // n
		CSEHelpersFilepath:                      "/opt/azure/containers/provision_source.sh",                         // s
		CSEDistroHelpersFilepath:                "/opt/azure/containers/provision_source_distro.sh",                  // s
		CSEInstallFilepath:                      "/opt/azure/containers/provision_installs.sh",                       // s
		CSEDistroInstallFilepath:                "/opt/azure/containers/provision_installs_distro.sh",                // s
		CSEConfigFilepath:                       "/opt/azure/containers/provision_configs.sh",                        // s
		AzurePrivateRegistryServer:              "",                                                                  // cd
		HasCustomSearchDomain:                   false,                                                               // cd
		CustomSearchDomainFilepath:              "/opt/azure/containers/setup-custom-search-domains.sh",              // s
		HTTPProxyURLs:                           "",                                                                  // cd
		HTTPSProxyURLs:                          "",                                                                  // cd
		NoProxyURLs:                             "",                                                                  // cd
		TLSBootstrappingEnabled:                 true,                                                                // s
		SecureTLSBootstrappingEnabled:           false,                                                               // s
		EnableKubeletServingCertificateRotation: false,                                                               // s
		THPEnabled:                              "",                                                                  // cd
		THPDefrag:                               "",                                                                  // cd
		ServicePrincipalFileContent:             base64.StdEncoding.EncodeToString([]byte("msi")),                    // s
		KubeletClientContent:                    "",                                                                  // -
		KubeletClientCertContent:                "",                                                                  // -
		KubeletConfigFileEnabled:                false,                                                               // s
		KubeletConfigFileContent:                "",                                                                  // s
		SwapFileSizeMB:                          0,                                                                   // td
		GPUInstanceProfile:                      "",                                                                  // td
		CustomSearchDomainName:                  "",                                                                  // cd
		CustomSearchRealmUser:                   "",                                                                  // cd
		CustomSearchRealmPassword:               "",                                                                  // cd
		MessageOfTheDay:                         "",                                                                  // td
		HasKubeletDiskType:                      false,                                                               // td
		SysctlContent:                           base64.StdEncoding.EncodeToString(sysctlContent),                    // td
		KubeletFlags:                            "",                                                                  // psX
		AzureEnvironmentFilepath:                "",                                                                  // s
		ContainerdConfigContent:                 "",                                                                  // kd
		IsKata:                                  false,                                                               // n
		NeedsCgroupV2:                           true,                                                                // s only static for karpenter
		EnsureNoDupePromiscuousBridge:           false,                                                               // s karpenter does not support kubenet
		EnableArtifactStreaming:                 false,                                                               // td
	}
}
