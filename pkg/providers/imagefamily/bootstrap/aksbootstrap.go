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
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AKS struct {
	Options

	Arch                           string
	TenantID                       string
	SubscriptionID                 string
	KubeletIdentityClientID        string
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
// p : user input, part of standard Nodepool CR spec. Example: custom labels, kubelet config
// t : user input, AKSNodeClass (potentially per node)
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
// AKSNodeClass                : user input that could be per-node (t) - ALL DEFAULTED FOR NOW
// Nodepool spec            : selected nodepool-level user input (p)

// NodeBootstrapVariables carries all variables needed to bootstrap a node
// It is used as input rendering the bootstrap script Go template (retrieved from getCustomDataTemplate)
type NodeBootstrapVariables struct {
	IsAKSCustomCloud                        bool     // n   (false)
	InitAKSCustomCloudFilepath              string   // n   (static)
	AKSCustomCloudRepoDepotEndpoint         string   // n   derived from custom cloud env?
	AdminUsername                           string   // t   typically azureuser but can be user input
	MobyVersion                             string   // -   unnecessary
	TenantID                                string   // p   environment derived, unnecessary?
	KubernetesVersion                       string   // ?   cluster/node pool specific, derived from user input
	HyperkubeURL                            string   // -   should be unnecessary
	KubeBinaryURL                           string   // -   necessary only for non-cached versions / static-ish
	CredentialProviderDownloadURL           string   // -	  necessary only for non-cached versions / static-ish
	CustomKubeBinaryURL                     string   // -   unnecessary
	KubeproxyURL                            string   // -   should be unnecessary or bug
	APIServerPublicKey                      string   // -   unique per cluster, actually not sure best way to extract? [should not be needed on agent nodes]
	SubscriptionID                          string   // a   can be derived from environment/imds
	ResourceGroup                           string   // a   can be derived from environment/imds
	Location                                string   // a   can be derived from environment/imds
	VMType                                  string   // xd  derived from cluster but unnecessary (?) only used by CCM [will default to "vmss" for now]
	Subnet                                  string   // xd  derived from cluster but unnecessary (?) only used by CCM [will default to "aks-subnet for now]
	NetworkSecurityGroup                    string   // xk  derived from cluster but unnecessary (?) only used by CCM [= "aks-agentpool-<clusterid>-nsg" for now]
	VirtualNetwork                          string   // xk  derived from cluster but unnecessary (?) only used by CCM [= "aks-vnet-<clusterid>" for now]
	VirtualNetworkResourceGroup             string   // xd  derived from cluster but unnecessary (?) only used by CCM [default to empty, looks like unused]
	RouteTable                              string   // xk  derived from cluster but unnecessary (?) only used by CCM [= "aks-agentpool-<clusterid>-routetable" for now]
	PrimaryAvailabilitySet                  string   // -   derived from cluster but unnecessary (?) only used by CCM
	PrimaryScaleSet                         string   // -   derived from cluster but unnecessary (?) only used by CCM
	ServicePrincipalClientID                string   // ad  user input
	NetworkPlugin                           string   // x   user input (? actually derived from cluster, right?)
	NetworkPolicy                           string   // x   user input / unique per cluster. user-specified.
	VNETCNILinuxPluginsURL                  string   // -   unnecessary [actually, currently required]
	CNIPluginsURL                           string   // -   unnecessary [actually, currently required]
	CloudProviderBackoff                    bool     // s   BEGIN CLOUD CONFIG for azure stuff, static/derived from user inputs
	CloudProviderBackoffMode                string   // s   [static until has to be exposed; could propagate Karpenter RL config, but won't]
	CloudProviderBackoffRetries             string   // s
	CloudProviderBackoffExponent            string   // s
	CloudProviderBackoffDuration            string   // s
	CloudProviderBackoffJitter              string   // s
	CloudProviderRatelimit                  bool     // s
	CloudProviderRatelimitQPS               string   // s
	CloudProviderRatelimitQPSWrite          string   // s
	CloudProviderRatelimitBucket            string   // s
	CloudProviderRatelimitBucketWrite       string   // s
	LoadBalancerDisableOutboundSNAT         bool     // xd  [= false for now]
	UseManagedIdentityExtension             bool     // s   [always true, as long as we only support managed identity]
	UseInstanceMetadata                     bool     // s   [always true?]
	LoadBalancerSKU                         string   // xd  [= "Standard" for now]
	ExcludeMasterFromStandardLB             bool     // s   [always true?]
	MaximumLoadbalancerRuleCount            int      // xd  END CLOUD CONFIG [will default to 250 for now]
	ContainerRuntime                        string   // s   always containerd
	CLITool                                 string   // s   static/unnecessary
	ContainerdDownloadURLBase               string   // -   unnecessary
	NetworkMode                             string   // c   user input
	UserAssignedIdentityID                  string   // a   user input
	APIServerName                           string   // x   unique per cluster
	IsVHD                                   bool     // s   static-ish
	GPUNode                                 bool     // k   derived from VM size
	SGXNode                                 bool     // -   unused
	MIGNode                                 bool     // t   user input
	ConfigGPUDriverIfNeeded                 bool     // s   depends on hardware, unnecessary for oss, but aks provisions gpu drivers
	EnableGPUDevicePluginIfNeeded           bool     // -   deprecated/preview only, don't do this for OSS
	TeleportdPluginDownloadURL              string   // -   user input, don't do this for OSS
	ContainerdVersion                       string   // -   unused
	ContainerdPackageURL                    string   // -   only for testing
	RuncVersion                             string   // -   unused
	RuncPackageURL                          string   // -   testing only
	EnableHostsConfigAgent                  bool     // n   derived from private cluster user input...I think?
	DisableSSH                              bool     // t   user input
	NeedsContainerd                         bool     // s   static true
	TeleportEnabled                         bool     // t   user input
	ShouldConfigureHTTPProxy                bool     // c   user input
	ShouldConfigureHTTPProxyCA              bool     // c   user input [secret]
	HTTPProxyTrustedCA                      string   // c   user input [secret]
	ShouldConfigureCustomCATrust            bool     // c   user input
	CustomCATrustConfigCerts                []string // c   user input [secret]
	IsKrustlet                              bool     // t   user input
	GPUNeedsFabricManager                   bool     // v   determined by GPU hardware type
	NeedsDockerLogin                        bool     // t   user input [still needed?]
	IPv6DualStackEnabled                    bool     // t   user input
	OutboundCommand                         string   // s   mostly static/can be
	EnableUnattendedUpgrades                bool     // c   user input [presumably cluster level, correct?]
	EnsureNoDupePromiscuousBridge           bool     // k   derived {{ and NeedsContainerd IsKubenet (not HasCalicoNetworkPolicy) }} [could be computed by template ...]
	ShouldConfigSwapFile                    bool     // t   user input
	ShouldConfigTransparentHugePage         bool     // t   user input
	TargetCloud                             string   // n   derive from environment/user input
	TargetEnvironment                       string   // n   derive from environment/user input
	CustomEnvJSON                           string   // n   derive from environment/user input
	IsCustomCloud                           bool     // n   derive from environment/user input
	CSEHelpersFilepath                      string   // s   static
	CSEDistroHelpersFilepath                string   // s   static
	CSEInstallFilepath                      string   // s   static
	CSEDistroInstallFilepath                string   // s   static
	CSEConfigFilepath                       string   // s   static
	AzurePrivateRegistryServer              string   // c   user input
	HasCustomSearchDomain                   bool     // c   user input
	CustomSearchDomainFilepath              string   // s   static
	HTTPProxyURLs                           string   // c   user input [presumably cluster-level]
	HTTPSProxyURLs                          string   // c   user input [presumably cluster-level]
	NoProxyURLs                             string   // c   user input [presumably cluster-level]
	TLSBootstrappingEnabled                 bool     // s   static true
	SecureTLSBootstrappingEnabled           bool     // s   static false
	EnableKubeletServingCertificateRotation bool     // s   static false
	DHCPv6ServiceFilepath                   string   // k   derived from user input [how?]
	DHCPv6ConfigFilepath                    string   // k   derived from user input [how?]
	THPEnabled                              string   // c   user input [presumably cluster-level][should be bool?]
	THPDefrag                               string   // c   user input [presumably cluster-level][should be bool?]
	ServicePrincipalFileContent             string   // s   only required for RP cluster [static: msi?]
	KubeletClientContent                    string   // -   unnecessary [if using TLS bootstrapping]
	KubeletClientCertContent                string   // -   unnecessary
	KubeletConfigFileEnabled                bool     // s   can be static	[should kubelet config be actually used/preferred instead of flags?]
	KubeletConfigFileContent                string   // s   mix of user/static/RP-generated.
	SwapFileSizeMB                          int      // t   user input
	GPUImageSHA                             string   // s	  static sha rarely updated
	GPUDriverVersion                        string   // k   determine by OS + GPU hardware requirements; can be determined automatically, but hard. suggest using GPU operator.
	GPUDriverType                           string   // k
	GPUInstanceProfile                      string   // t   user-specified
	CustomSearchDomainName                  string   // c   user-specified [presumably cluster-level]
	CustomSearchRealmUser                   string   // c   user-specified [presumably cluster-level]
	CustomSearchRealmPassword               string   // c   user-specified [presumably cluster-level]
	MessageOfTheDay                         string   // t   user-specified [presumably node-level]
	HasKubeletDiskType                      bool     // t   user-specified [presumably node-level]
	NeedsCgroupV2                           bool     // k   can be automatically determined
	SysctlContent                           string   // t   user-specified
	TLSBootstrapToken                       string   // X   nodepool or node specific. can be created automatically
	KubeletFlags                            string   // psX unique per nodepool. partially user-specified, static, and RP-generated
	KubeletNodeLabels                       string   // pk  node-pool specific. user-specified.
	AzureEnvironmentFilepath                string   // s   can be made static [usually "/etc/kubernetes/azure.json", but my examples use ""?]
	KubeCACrt                               string   // x   unique per cluster
	ContainerdConfigContent                 string   // k   determined by GPU VM size, WASM support, Kata support
	IsKata                                  bool     // n   user-specified
}

func (a AKS) aksBootstrapScript() (string, error) {
	// use these as the base / defaults
	nbv := getStaticNodeBootstrapVars()

	// apply overrides from passed in options
	a.applyOptions(nbv)

	containerdConfigTemplate, err := containerdConfigFromNodeBootstrapVars(nbv)
	if err != nil {
		return "", fmt.Errorf("error getting containerd config from node bootstrap variables: %w", err)
	}

	nbv.ContainerdConfigContent = base64.StdEncoding.EncodeToString([]byte(containerdConfigTemplate))
	// generate script from template using the variables
	customData, err := getCustomDataFromNodeBootstrapVars(nbv)
	if err != nil {
		return "", fmt.Errorf("error getting custom data from node bootstrap variables: %w", err)
	}
	return customData, nil
}

// Download URL for KUBE_BINARY_URL publishes each k8s version in the URL.
func kubeBinaryURL(kubernetesVersion, cpuArch string) string {
	return fmt.Sprintf("%s/kubernetes/v%s/binaries/kubernetes-node-linux-%s.tar.gz", globalAKSMirror, kubernetesVersion, cpuArch)
}

// CredentialProviderURL returns the URL for OOT credential provider,
// or an empty string if OOT provider is not to be used
func CredentialProviderURL(kubernetesVersion, arch string) string {
	minorVersion := semver.MustParse(kubernetesVersion).Minor
	if minorVersion < 30 { // use from 1.30; 1.29 supports it too, but we have not fully tested it with Karpenter
		return ""
	}

	// credential provider has its own release outside of k8s version, and there'll be one credential provider binary for each k8s release,
	// as credential provider release goes with cloud-provider-azure, not every credential provider release will be picked up unless
	// there are CVE or bug fixes.
	var credentialProviderVersion string
	switch minorVersion {
	case 29:
		credentialProviderVersion = "1.29.15"
	case 30:
		credentialProviderVersion = "1.30.12"
	case 31:
		credentialProviderVersion = "1.31.6"
	case 32:
		credentialProviderVersion = "1.32.5"
	case 33:
		fallthrough // to default, which is same as latest
	default:
		credentialProviderVersion = "1.33.0"
	}

	return fmt.Sprintf("%s/cloud-provider-azure/v%s/binaries/azure-acr-credential-provider-linux-%s-v%s.tar.gz", globalAKSMirror, credentialProviderVersion, arch, credentialProviderVersion)
}

func (a AKS) applyOptions(nbv *NodeBootstrapVariables) {
	nbv.KubeCACrt = *a.CABundle
	nbv.APIServerName = a.APIServerName
	nbv.TLSBootstrapToken = a.KubeletClientTLSBootstrapToken

	nbv.TenantID = a.TenantID
	nbv.SubscriptionID = a.SubscriptionID
	nbv.Location = a.Location
	nbv.ResourceGroup = a.ResourceGroup
	nbv.UserAssignedIdentityID = a.KubeletIdentityClientID

	nbv.NetworkPlugin = a.NetworkPlugin

	nbv.NetworkPolicy = a.NetworkPolicy
	nbv.KubernetesVersion = a.KubernetesVersion

	nbv.KubeBinaryURL = kubeBinaryURL(a.KubernetesVersion, a.Arch)
	nbv.VNETCNILinuxPluginsURL = fmt.Sprintf("%s/azure-cni/v1.4.32/binaries/azure-vnet-cni-linux-%s-v1.4.32.tgz", globalAKSMirror, a.Arch)
	nbv.CNIPluginsURL = fmt.Sprintf("%s/cni-plugins/v1.1.1/binaries/cni-plugins-linux-%s-v1.1.1.tgz", globalAKSMirror, a.Arch)
	// calculated values
	nbv.NetworkSecurityGroup = fmt.Sprintf("aks-agentpool-%s-nsg", a.ClusterID)
	nbv.RouteTable = fmt.Sprintf("aks-agentpool-%s-routetable", a.ClusterID)

	if a.GPUNode {
		nbv.GPUNode = true
		nbv.ConfigGPUDriverIfNeeded = true
		nbv.GPUDriverVersion = a.GPUDriverVersion
		nbv.GPUDriverType = a.GPUDriverType
		nbv.GPUImageSHA = a.GPUImageSHA
	}

	// merge and stringify labels
	kubeletLabels := a.Labels

	subnetParts, _ := utils.GetVnetSubnetIDComponents(a.SubnetID)
	nbv.Subnet = subnetParts.SubnetName
	nbv.VirtualNetworkResourceGroup = subnetParts.ResourceGroupName
	nbv.VirtualNetwork = subnetParts.VNetName

	nbv.KubeletNodeLabels = strings.Join(lo.MapToSlice(kubeletLabels, func(k, v string) string {
		return fmt.Sprintf("%s=%s", k, v)
	}), ",")

	// Assign Per K8s version kubelet flags
	minorVersion := semver.MustParse(a.KubernetesVersion).Minor
	kubeletFlagsBase := getBaseKubeletFlags()
	if minorVersion < 31 {
		kubeletFlagsBase["--keep-terminated-pod-volumes"] = "false"
	}
	if minorVersion >= 34 {
		delete(kubeletFlagsBase, "--cloud-config") // removed in 1.34
	}

	credentialProviderURL := CredentialProviderURL(a.KubernetesVersion, a.Arch)
	if credentialProviderURL != "" { // use OOT credential provider
		nbv.CredentialProviderDownloadURL = credentialProviderURL
		kubeletFlagsBase["--image-credential-provider-config"] = "/var/lib/kubelet/credential-provider-config.yaml"
		kubeletFlagsBase["--image-credential-provider-bin-dir"] = "/var/lib/kubelet/credential-provider"
	} else { // Versions Less than 1.30
		// we can make this logic smarter later when we have more than one
		// for now just adding here.
		kubeletFlagsBase["--feature-gates"] = "DisableKubeletCloudCredentialProviders=false"
		kubeletFlagsBase["--azure-container-registry-config"] = "/etc/kubernetes/azure.json"
	}
	// merge and stringify taints
	kubeletFlags := lo.Assign(kubeletFlagsBase)
	if len(a.Taints) > 0 {
		taintStrs := lo.Map(a.Taints, func(taint v1.Taint, _ int) string { return taint.ToString() })
		kubeletFlags = lo.Assign(kubeletFlags, map[string]string{"--register-with-taints": strings.Join(taintStrs, ",")})
	}

	nodeclaimKubeletConfig := kubeletConfigToMap(a.KubeletConfig)
	kubeletFlags = lo.Assign(kubeletFlags, nodeclaimKubeletConfig)

	// stringify kubelet flags (including taints)
	nbv.KubeletFlags = strings.Join(lo.MapToSlice(kubeletFlags, func(k, v string) string {
		return fmt.Sprintf("%s=%s", k, v)
	}), " ")
}

func containerdConfigFromNodeBootstrapVars(nbv *NodeBootstrapVariables) (string, error) {
	var buffer bytes.Buffer
	if err := getContainerdConfigTemplate().Execute(&buffer, *nbv); err != nil {
		return "", fmt.Errorf("error executing containerd config template: %w", err)
	}
	return buffer.String(), nil
}

func getCustomDataFromNodeBootstrapVars(nbv *NodeBootstrapVariables) (string, error) {
	var buffer bytes.Buffer
	if err := getCustomDataTemplate().Execute(&buffer, *nbv); err != nil {
		return "", fmt.Errorf("error executing custom data template: %w", err)
	}
	return buffer.String(), nil
}

// nolint: gocyclo
func kubeletConfigToMap(kubeletConfig *KubeletConfiguration) map[string]string {
	args := make(map[string]string)

	if kubeletConfig == nil {
		return args
	}
	args["--max-pods"] = fmt.Sprintf("%d", kubeletConfig.MaxPods)
	JoinParameterArgsToMap(args, "--system-reserved", kubeletConfig.SystemReserved, "=")
	JoinParameterArgsToMap(args, "--kube-reserved", kubeletConfig.KubeReserved, "=")
	JoinParameterArgsToMap(args, "--eviction-hard", kubeletConfig.EvictionHard, "<")
	JoinParameterArgsToMap(args, "--eviction-soft", kubeletConfig.EvictionSoft, "<")
	JoinParameterArgsToMap(args, "--eviction-soft-grace-period", lo.MapValues(kubeletConfig.EvictionSoftGracePeriod, func(v metav1.Duration, _ string) string {
		return v.Duration.String()
	}), "=")

	if kubeletConfig.EvictionMaxPodGracePeriod != nil {
		args["--eviction-max-pod-grace-period"] = fmt.Sprintf("%d", lo.FromPtr(kubeletConfig.EvictionMaxPodGracePeriod))
	}
	if kubeletConfig.ImageGCHighThresholdPercent != nil {
		args["--image-gc-high-threshold"] = fmt.Sprintf("%d", lo.FromPtr(kubeletConfig.ImageGCHighThresholdPercent))
	}
	if kubeletConfig.ImageGCLowThresholdPercent != nil {
		args["--image-gc-low-threshold"] = fmt.Sprintf("%d", lo.FromPtr(kubeletConfig.ImageGCLowThresholdPercent))
	}
	if kubeletConfig.CPUCFSQuota != nil {
		args["--cpu-cfs-quota"] = fmt.Sprintf("%t", lo.FromPtr(kubeletConfig.CPUCFSQuota))
	}
	if kubeletConfig.CPUManagerPolicy != "" {
		args["--cpu-manager-policy"] = kubeletConfig.CPUManagerPolicy
	}
	if kubeletConfig.TopologyManagerPolicy != "" {
		args["--topology-manager-policy"] = kubeletConfig.TopologyManagerPolicy
	}
	if kubeletConfig.ContainerLogMaxSize != "" {
		args["--container-log-max-size"] = kubeletConfig.ContainerLogMaxSize
	}
	if kubeletConfig.ContainerLogMaxFiles != nil {
		args["--container-log-max-files"] = fmt.Sprintf("%d", lo.FromPtr(kubeletConfig.ContainerLogMaxFiles))
	}
	if kubeletConfig.PodPidsLimit != nil {
		args["--pod-max-pids"] = fmt.Sprintf("%d", lo.FromPtr(kubeletConfig.PodPidsLimit))
	}
	if len(kubeletConfig.AllowedUnsafeSysctls) > 0 {
		args["--allowed-unsafe-sysctls"] = strings.Join(kubeletConfig.AllowedUnsafeSysctls, ",")
	}
	if kubeletConfig.ClusterDNSServiceIP != "" {
		args["--cluster-dns"] = kubeletConfig.ClusterDNSServiceIP
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
