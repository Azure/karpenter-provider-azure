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
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"knative.dev/pkg/ptr"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AKS struct {
	Options

	Arch                           string
	TenantID                       string
	SubscriptionID                 string
	UserAssignedIdentityID         string
	Location                       string
	ResourceGroup                  string
	ClusterID                      string
	APIServerName                  string
	KubeletClientTLSBootstrapToken string
	NetworkPlugin                  string
	NetworkPolicy                  string
	KubernetesVersion              string
}

var _ Bootstrapper = (*AKS)(nil) // assert AKS implements Bootstrapper

func (a AKS) Script() (string, error) {
	bootstrapScript, err := a.aksBootstrapScript()
	if err != nil {
		return "", fmt.Errorf("error getting AKS bootstrap script: %w", err)
	}

	return base64.StdEncoding.EncodeToString([]byte(bootstrapScript)), nil
}

var (
	//go:embed cse_cmd.sh.gtpl
	customDataTemplateText string
	customDataTemplate     = template.Must(template.New("customdata").Parse(customDataTemplateText))

	//go:embed cse_cmd_nbcontract.sh.gtpl
	customDataTemplateTextNBContract string
	customDataTemplateNBContract     = template.Must(template.New("customdata").Funcs(getFuncMap()).Parse(customDataTemplateTextNBContract))

	//go:embed  containerd.toml.gtpl
	containerdConfigTemplateText string
	containerdConfigTemplate     = template.Must(template.New("containerdconfig").Parse(containerdConfigTemplateText))

	//go:embed sysctl.conf
	sysctlContent []byte
	//go:embed kubenet-cni.json.gtpl
	kubenetTemplate []byte

	// source note: unique per nodepool. partially user-specified, static, and RP-generated
	// removed --image-pull-progress-deadline=30m  (not in 1.24?)
	// removed --network-plugin=cni (not in 1.24?)
	kubeletFlagsBase = map[string]string{
		"--address":                           "0.0.0.0",
		"--anonymous-auth":                    "false",
		"--authentication-token-webhook":      "true",
		"--authorization-mode":                "Webhook",
		"--azure-container-registry-config":   "/etc/kubernetes/azure.json",
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
		"--keep-terminated-pod-volumes":       "false",
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

	kubeletNodeLabelsBase = map[string]string{
		"kubernetes.azure.com/mode": "user",
	}
	vnetCNILinuxPluginsURL = fmt.Sprintf("%s/azure-cni/v1.4.32/binaries/azure-vnet-cni-linux-amd64-v1.4.32.tgz", globalAKSMirror)
	cniPluginsURL          = fmt.Sprintf("%s/cni-plugins/v1.1.1/binaries/cni-plugins-linux-amd64-v1.1.1.tgz", globalAKSMirror)
)

var (
	enabledFeatureState  = getFeatureState(true)
	disabledFeatureState = getFeatureState(false)
	defaultSwapFileSize  = int32(0)

	// Config item types classified by code:
	//
	// - : known unnecessary or unused - (empty) value set in code, until dropped from template
	// n : not (yet?) supported, set to empty or something reasonable in code
	// s : static/constant (or very slow changing), value set in code;
	//     also the choice for something that does not have to be exposed for customization yet
	//
	// a : known argument/parameter, passed in (usually from environment)
	// x : unique per cluster,  extracted or specified. (Candidates for exposure/accessibility via API)
	// X : unique per nodepool, extracted or specified. (Candidates for exposure/accessibility via API)
	// c : user input, Options (provider-specific), e.g., could be from environment variables
	// p : user input, part of standard Provisioner (NodePool) CR spec. Example: custom labels, kubelet config
	// t : user input, NodeTemplate (potentially per node)
	// k : computed (at runtime) by Karpenter (e.g. based on VM SKU, extra labels, etc.)
	//     (xk - computed from per cluster data, such as cluster id)
	//
	// ? : needs more investigation
	//
	// multiple codes: combined from several sources

	// Config sources for types:
	//
	// Hardcoded (this file)       : unused (-), static (s) and unsupported (n), as well as selected defaults (s)
	// Computed at runtime         : computed (k)
	// Options (provider-specific) : cluster-level user input (c) - ALL DEFAULTED FOR NOW
	//                             : as well as unique per cluster (x) - until we have a better place for these
	// (TBD)                       : unique per nodepool. extracted or specified (X)
	// NodeTemplate                : user input that could be per-node (t) - ALL DEFAULTED FOR NOW
	// Provisioner spec            : selected nodepool-level user input (p)

	// NodeBootstrapVariables carries all variables needed to bootstrap a node
	// It is used as input rendering the bootstrap script Go template (customDataTemplate)
	// baseline, covering unused (-), static (s), and unsupported (n) fields,
	// as well as defaults, cluster/node level (cd/td/xd)
	staticNodeBootstrapVars = nbcontractv1.Configuration{
		CustomCloudConfig: &nbcontractv1.CustomCloudConfig{
			Status:               &disabledFeatureState, //n
			InitFilePath:         ptr.String(""),        //n
			RepoDepotEndpoint:    ptr.String(""),        //n
			TargetEnvironment:    "AzurePublicCloud",    //n
			TargetCloud:          "AzurePublicCloud",    //n
			CustomEnvJsonContent: "",                    //n
		},
		LinuxAdminUsername: "azureuser", // td
		KubeBinaryConfig: &nbcontractv1.KubeBinaryConfig{
			KubeBinaryUrl:        "", // cd
			CustomKubeBinaryUrl:  "", // -
			PrivateKubeBinaryUrl: "",
		},
		KubeproxyUrl: "", // -
		ApiserverConfig: &nbcontractv1.ApiServerConfig{
			ApiserverPublicKey: "", // not initialized anywhere?
			ApiserverName:      "", // xd
		},
		VmType: "vmss", // xd
		NetworkConfig: &nbcontractv1.NetworkConfig{
			NetworkMode: getNetworkModeType(""), // cd
			Subnet:      "aks-subnet",           // xd
			VirtualNetworkConfig: &nbcontractv1.VirtualNetworkConfig{
				ResourceGroup: "", // xd
			},
			VnetCniPluginsUrl: vnetCNILinuxPluginsURL, // - [currently required, installCNI in provisioning scripts depends on CNI_PLUGINS_URL]
			CniPluginsUrl:     cniPluginsURL,          // - [currently required, same]
		},
		PrimaryAvailabilitySet: "",   // -
		PrimaryScaleSet:        "",   // -
		UseInstanceMetadata:    true, // s
		LoadBalancerConfig: &nbcontractv1.LoadBalancerConfig{
			LoadBalancerSku:                       getLoadBalancerSKU("Standard"), // xd
			ExcludeMasterFromStandardLoadBalancer: true,                           //s
			MaxLoadBalancerRuleCount:              int32(250),                     // xd
		},
		ContainerdConfig: &nbcontractv1.ContainerdConfig{
			ContainerdDownloadUrlBase: "", // -
			ContainerdVersion:         "", // -
			ContainerdPackageUrl:      "", // -
		},
		IsVhd:     true,  // s
		IsSgxNode: false, // -
		GpuConfig: &nbcontractv1.GPUConfig{
			NvidiaState:        &disabledFeatureState, // td
			ConfigGpuDriver:    &enabledFeatureState,  // s
			GpuDevicePlugin:    &disabledFeatureState, // -
			GpuInstanceProfile: ptr.String(""),        // td
			GpuImageSha:        ptr.String(""),        // s
		},
		TeleportConfig: &nbcontractv1.TeleportConfig{
			TeleportdPluginDownloadUrl: "",                   // -
			Status:                     disabledFeatureState, // td
		},
		RuncConfig: &nbcontractv1.RuncConfig{
			RuncVersion:    "", // -
			RuncPackageUrl: "", // -
		},
		SshStatus:              enabledFeatureState,  // td
		HostsConfigAgentStatus: disabledFeatureState, // n
		HttpProxyConfig: &nbcontractv1.HTTPProxyConfig{
			Status:         &disabledFeatureState, // cd
			HttpProxy:      "",                    // cd
			HttpsProxy:     "",                    // cd
			NoProxyEntries: []string{""},          // cd
			ProxyTrustedCa: ptr.String(""),        // cd
			CaStatus:       &disabledFeatureState, // cd
		},
		CustomCaTrustConfig: &nbcontractv1.CustomCATrustConfig{
			Status:        disabledFeatureState, // cd
			CustomCaCerts: []string{},           // cd
		},
		OutboundCommand:            ptr.String("curl -v --insecure --proxy-insecure https://mcr.microsoft.com/v2/"), // s
		UnattendedUpgradeStatus:    &disabledFeatureState,                                                           // cd
		IsKrustlet:                 false,                                                                           // n                                                     // td
		AzurePrivateRegistryServer: "",                                                                              // cd
		CustomSearchDomain: &nbcontractv1.CustomSearchDomain{
			CustomSearchDomainFilepath:      "/opt/azure/containers/setup-custom-search-domains.sh", // s
			CustomSearchDomainName:          "",                                                     // cd
			CustomSearchDomainRealmUser:     "",                                                     // cd
			CustomSearchDomainRealmPassword: "",                                                     // cd
		},
		CustomLinuxOsConfig: &nbcontractv1.CustomLinuxOSConfig{
			SwapFileSize:               &defaultSwapFileSize, // td
			TransparentHugepageSupport: ptr.String(""),       // cd
			TransparentDefrag:          ptr.String(""),       // cd
		},
		KubeletConfig: &nbcontractv1.KubeletConfig{
			KubeletClientKey:         "",                   // -
			KubeletClientCertContent: "",                   // -
			KubeletConfigFileStatus:  disabledFeatureState, // s
			KubeletConfigFileContent: "",                   // s
			KubeletFlags:             map[string]string{},  // psX
		},
		TlsBootstrappingConfig: &nbcontractv1.TLSBootstrappingConfig{
			TlsBootstrappingMode: nbcontractv1.BootstrappingMode_STANDARD,
		},
		MessageOfTheDay: "",    // td
		IsKata:          false, // n
	}
)

// Node Labels for Vnet
const (
	vnetDataPlaneLabel      = "kubernetes.azure.com/ebpf-dataplane"
	vnetNetworkNameLabel    = "kubernetes.azure.com/network-name"
	vnetSubnetNameLabel     = "kubernetes.azure.com/network-subnet"
	vnetSubscriptionIDLabel = "kubernetes.azure.com/network-subscription"
	vnetGUIDLabel           = "kubernetes.azure.com/nodenetwork-vnetguid"
	vnetPodNetworkTypeLabel = "kubernetes.azure.com/podnetwork-type"
	ciliumDataPlane         = "cilium"
	overlayNetworkType      = "overlay"
	globalAKSMirror         = "https://acs-mirror.azureedge.net"
)

func (a AKS) aksBootstrapScript() (string, error) {
	// use these as the base / defaults
	nbv := staticNodeBootstrapVars // don't need deep copy (yet)

	// apply overrides from passed in options
	a.applyOptions(&nbv)

	customDataNbContract, err := getCustomDataFromNodeBootstrapContract(&nbv)
	if err != nil {
		return "", fmt.Errorf("error getting custom data nbcontract from node bootstrap variables: %w", err)
	}
	return customDataNbContract, nil
}

// Download URL for KUBE_BINARY_URL publishes each k8s version in the URL.
func kubeBinaryURL(kubernetesVersion, cpuArch string) string {
	return fmt.Sprintf("%s/kubernetes/v%s/binaries/kubernetes-node-linux-%s.tar.gz", globalAKSMirror, kubernetesVersion, cpuArch)
}

func (a AKS) applyOptions(nbv *nbcontractv1.Configuration) {
	nbv.ClusterCertificateAuthority = *a.CABundle
	nbv.ApiserverConfig.ApiserverName = a.APIServerName
	nbv.TlsBootstrappingConfig.TlsBootstrapToken = a.KubeletClientTLSBootstrapToken

	nbv.TenantId = a.TenantID
	nbv.SubscriptionId = a.SubscriptionID
	nbv.Location = a.Location
	nbv.ResourceGroup = a.ResourceGroup
	servicePrincipalClientID := "msi"
	servicePrincipalFileContent := base64.StdEncoding.EncodeToString([]byte("msi"))
	nbv.IdentityConfig = getIdentityConfig(servicePrincipalClientID, servicePrincipalFileContent, a.UserAssignedIdentityID)

	nbv.NetworkConfig.NetworkPlugin = getNetworkPluginType(a.NetworkPlugin)
	nbv.NetworkConfig.NetworkPolicy = getNetworkPolicyType(a.NetworkPolicy)
	nbv.KubernetesVersion = a.KubernetesVersion

	nbv.KubeBinaryConfig.KubeBinaryUrl = kubeBinaryURL(a.KubernetesVersion, a.Arch)
	nbv.NetworkConfig.VnetCniPluginsUrl = fmt.Sprintf("%s/azure-cni/v1.4.32/binaries/azure-vnet-cni-linux-%s-v1.4.32.tgz", globalAKSMirror, a.Arch)
	nbv.NetworkConfig.CniPluginsUrl = fmt.Sprintf("%s/cni-plugins/v1.1.1/binaries/cni-plugins-linux-%s-v1.1.1.tgz", globalAKSMirror, a.Arch)

	// calculated values
	noDupePromiscuousBridge := nbv.NeedsContainerd && nbv.NetworkConfig.NetworkPlugin == nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_KUBENET && nbv.NetworkConfig.NetworkPolicy != nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_CALICO
	nbv.EnsureNoDupePromiscuousBridge = &noDupePromiscuousBridge
	nbv.NetworkConfig.NetworkSecurityGroup = fmt.Sprintf("aks-agentpool-%s-nsg", a.ClusterID)
	nbv.NetworkConfig.VirtualNetworkConfig.Name = fmt.Sprintf("aks-vnet-%s", a.ClusterID)
	nbv.NetworkConfig.RouteTable = fmt.Sprintf("aks-agentpool-%s-routetable", a.ClusterID)

	if a.GPUNode {
		nbv.GpuConfig.NvidiaState = &enabledFeatureState
		nbv.GpuConfig.ConfigGpuDriver = &enabledFeatureState
		nbv.GpuConfig.GpuImageSha = &a.GPUImageSHA
	}
	nbv.NeedsCgroupv2 = true
	// merge and stringify labels
	kubeletLabels := lo.Assign(kubeletNodeLabelsBase, a.Labels)
	getAgentbakerGeneratedLabels(a.ResourceGroup, kubeletLabels)

	//Adding vnet-related labels to the nodeLabels.
	azureVnetGUID := os.Getenv("AZURE_VNET_GUID")
	azureVnetName := os.Getenv("AZURE_VNET_NAME")
	azureSubnetName := os.Getenv("AZURE_SUBNET_NAME")

	vnetLabels := map[string]string{
		vnetDataPlaneLabel:      ciliumDataPlane,
		vnetNetworkNameLabel:    azureVnetName,
		vnetSubnetNameLabel:     azureSubnetName,
		vnetSubscriptionIDLabel: a.SubscriptionID,
		vnetGUIDLabel:           azureVnetGUID,
		vnetPodNetworkTypeLabel: overlayNetworkType,
	}

	kubeletLabels = lo.Assign(kubeletLabels, vnetLabels)
	nbv.KubeletConfig.KubeletNodeLabels = kubeletLabels

	// merge and stringify taints
	kubeletFlags := lo.Assign(kubeletFlagsBase)
	if len(a.Taints) > 0 {
		taintStrs := lo.Map(a.Taints, func(taint v1.Taint, _ int) string { return taint.ToString() })
		kubeletFlags = lo.Assign(kubeletFlags, map[string]string{"--register-with-taints": strings.Join(taintStrs, ",")})
	}

	machineKubeletConfig := KubeletConfigToMap(a.KubeletConfig)
	kubeletFlags = lo.Assign(kubeletFlags, machineKubeletConfig)
	nbv.KubeletConfig.KubeletFlags = kubeletFlags
}

func getCustomDataFromNodeBootstrapContract(nbcp *nbcontractv1.Configuration) (string, error) {
	var buffer bytes.Buffer
	if err := customDataTemplateNBContract.Execute(&buffer, nbcp); err != nil {
		return "", fmt.Errorf("error executing custom data NbContract template: %w", err)
	}
	return buffer.String(), nil
}

func getAgentbakerGeneratedLabels(nodeResourceGroup string, nodeLabels map[string]string) {
	nodeLabels["kubernetes.azure.com/role"] = "agent"
	nodeLabels["kubernetes.azure.com/cluster"] = normalizeResourceGroupNameForLabel(nodeResourceGroup)
}

func normalizeResourceGroupNameForLabel(resourceGroupName string) string {
	truncated := resourceGroupName
	truncated = strings.ReplaceAll(truncated, "(", "-")
	truncated = strings.ReplaceAll(truncated, ")", "-")
	const maxLen = 63
	if len(truncated) > maxLen {
		truncated = truncated[0:maxLen]
	}

	if strings.HasSuffix(truncated, "-") ||
		strings.HasSuffix(truncated, "_") ||
		strings.HasSuffix(truncated, ".") {
		if len(truncated) > 62 {
			return truncated[0:len(truncated)-1] + "z"
		}
		return truncated + "z"
	}
	return truncated
}

func KubeletConfigToMap(kubeletConfig *corev1beta1.KubeletConfiguration) map[string]string {
	args := make(map[string]string)

	if kubeletConfig == nil {
		return args
	}
	if kubeletConfig.MaxPods != nil {
		args["--max-pods"] = fmt.Sprintf("%d", ptr.Int32Value(kubeletConfig.MaxPods))
	}
	if kubeletConfig.PodsPerCore != nil {
		args["--pods-per-core"] = fmt.Sprintf("%d", ptr.Int32Value(kubeletConfig.PodsPerCore))
	}
	JoinParameterArgsToMap(args, "--system-reserved", resources.StringMap(kubeletConfig.SystemReserved), "=")
	JoinParameterArgsToMap(args, "--kube-reserved", resources.StringMap(kubeletConfig.KubeReserved), "=")
	JoinParameterArgsToMap(args, "--eviction-hard", kubeletConfig.EvictionHard, "<")
	JoinParameterArgsToMap(args, "--eviction-soft", kubeletConfig.EvictionSoft, "<")
	JoinParameterArgsToMap(args, "--eviction-soft-grace-period", lo.MapValues(kubeletConfig.EvictionSoftGracePeriod, func(v metav1.Duration, _ string) string {
		return v.Duration.String()
	}), "=")

	if kubeletConfig.EvictionMaxPodGracePeriod != nil {
		args["--eviction-max-pod-grace-period"] = fmt.Sprintf("%d", ptr.Int32Value(kubeletConfig.EvictionMaxPodGracePeriod))
	}
	if kubeletConfig.ImageGCHighThresholdPercent != nil {
		args["--image-gc-high-threshold"] = fmt.Sprintf("%d", ptr.Int32Value(kubeletConfig.ImageGCHighThresholdPercent))
	}
	if kubeletConfig.ImageGCLowThresholdPercent != nil {
		args["--image-gc-low-threshold"] = fmt.Sprintf("%d", ptr.Int32Value(kubeletConfig.ImageGCLowThresholdPercent))
	}
	if kubeletConfig.CPUCFSQuota != nil {
		args["--cpu-cfs-quota"] = fmt.Sprintf("%t", lo.FromPtr(kubeletConfig.CPUCFSQuota))
	}

	return args
}

// joinParameterArgsToMap joins a map of keys and values by their separator. The separator will sit between the
// arguments in a comma-separated list i.e. arg1<sep>val1,arg2<sep>val2
func JoinParameterArgsToMap[K comparable, V any](result map[string]string, name string, m map[K]V, separator string) {
	var args []string

	for k, v := range m {
		args = append(args, fmt.Sprintf("%v%s%v", k, separator, v))
	}
	if len(args) > 0 {
		result[name] = strings.Join(args, ",")
	}
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

// Will move to helper function
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

// Will move to helper function
// getLoadBalancerSKI returns the LoadBalancerSku enum based on the input string.
func getLoadBalancerSKU(sku string) nbcontractv1.LoadBalancerSku {
	if strings.EqualFold(sku, "Standard") {
		return nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_STANDARD
	} else if strings.EqualFold(sku, "Basic") {
		return nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_BASIC
	}

	return nbcontractv1.LoadBalancerSku_LOAD_BALANCER_SKU_UNSPECIFIED
}

// Will move to helper function
// getNetworkModeType returns the NetworkMode enum based on the input string.
func getNetworkModeType(networkMode string) nbcontractv1.NetworkModeType {
	if strings.EqualFold(networkMode, "transparent") {
		return nbcontractv1.NetworkModeType_NETWORK_MODE_TRANSPARENT
	} else if strings.EqualFold(networkMode, "l2bridge") {
		return nbcontractv1.NetworkModeType_NETWORK_MODE_L2BRIDGE
	}

	return nbcontractv1.NetworkModeType_NETWORK_MODE_UNSPECIFIED
}

// Will move to helper function
// getNetworkPluginType returns the NetworkPluginType enum based on the input string.
func getNetworkPluginType(networkPlugin string) nbcontractv1.NetworkPluginType {
	if strings.EqualFold(networkPlugin, "azure") {
		return nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_AZURE
	} else if strings.EqualFold(networkPlugin, "kubenet") {
		return nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_KUBENET
	}

	return nbcontractv1.NetworkPluginType_NETWORK_PLUGIN_TYPE_NONE
}

// Will move to helper function
func getNetworkPolicyType(networkPolicy string) nbcontractv1.NetworkPolicyType {
	if strings.EqualFold(networkPolicy, "azure") {
		return nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_AZURE
	} else if strings.EqualFold(networkPolicy, "calico") {
		return nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_CALICO
	}

	return nbcontractv1.NetworkPolicyType_NETWORK_POLICY_TYPE_NONE
}
