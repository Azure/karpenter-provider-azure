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

/* All the helper functions should be hosted by another public repo later. (e.g. agentbaker)
This action of populating cse_cmd.sh should happen in the Go binary on VHD.
Therefore, Karpenter will not use these helper functions once the Go binary is ready. */

// Parser helpers are used to get values of the env variables to populate cse_cmd.sh. For example, default values, values computed by others, etc.
// It's the go binary parser who will call these functions.

package bootstrap

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"text/template"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/blang/semver"
)

var (
	//go:embed kubenet-cni.json.gtpl
	kubenetTemplateContent []byte
	//go:embed  containerdfornbcontract.toml.gtpl
	containerdConfigTemplateTextForNBContract string
	containerdConfigTemplateForNBContract     = template.Must(
		template.New("containerdconfigfornbcontract").Funcs(getFuncMapForContainerdConfigTemplate()).Parse(containerdConfigTemplateTextForNBContract),
	)
)

func getFuncMap() template.FuncMap {
	return template.FuncMap{
		"derefString":                               deref[string],
		"derefBool":                                 deref[bool],
		"getStringFromVMType":                       getStringFromVMType,
		"getStringFromNetworkPluginType":            getStringFromNetworkPluginType,
		"getStringFromNetworkPolicyType":            getStringFromNetworkPolicyType,
		"getStringFromLoadBalancerSkuType":          getStringFromLoadBalancerSkuType,
		"getKubenetTemplate":                        getKubenetTemplate,
		"getSysctlContent":                          getSysctlContent,
		"getUlimitContent":                          getUlimitContent,
		"getContainerdConfig":                       getContainerdConfig,
		"getStringifiedStringArray":                 getStringifiedStringArray,
		"getIsMIGNode":                              getIsMIGNode,
		"getCustomCACertsStatus":                    getCustomCACertsStatus,
		"getEnableTLSBootstrap":                     getEnableTLSBootstrap,
		"getEnableSecureTLSBootstrap":               getEnableSecureTLSBootstrap,
		"getTLSBootstrapToken":                      getTLSBootstrapToken,
		"getCustomSecureTLSBootstrapAADServerAppID": getCustomSecureTLSBootstrapAADServerAppID,
		"getIsKrustlet":                             getIsKrustlet,
		"getEnsureNoDupePromiscuousBridge":          getEnsureNoDupePromiscuousBridge,
		"getHasSearchDomain":                        getHasSearchDomain,
		"getCSEHelpersFilepath":                     getCSEHelpersFilepath,
		"getCSEDistroHelpersFilepath":               getCSEDistroHelpersFilepath,
		"getCSEInstallFilepath":                     getCSEInstallFilepath,
		"getCSEDistroInstallFilepath":               getCSEDistroInstallFilepath,
		"getCSEConfigFilepath":                      getCSEConfigFilepath,
		"getCustomSearchDomainFilepath":             getCustomSearchDomainFilepath,
		"getDHCPV6ConfigFilepath":                   getDHCPV6ConfigFilepath,
		"getDHCPV6ServiceFilepath":                  getDHCPV6ServiceFilepath,
		"getShouldConfigContainerdUlimits":          getShouldConfigContainerdUlimits,
		"getKubeletConfigFileEnabled":               getKubeletConfigFileEnabled,
		"createSortedKeyValueStringPairs":           createSortedKeyValuePairs[string],
		"createSortedKeyValueInt32Pairs":            createSortedKeyValuePairs[int32],
		"getExcludeMasterFromStandardLB":            getExcludeMasterFromStandardLB,
		"getMaxLBRuleCount":                         getMaxLBRuleCount,
		"getGpuNode":                                getGpuNode,
		"getGpuImageSha":                            getGpuImageSha,
		"getGpuDriverVersion":                       getGpuDriverVersion,
		"getIsSgxEnabledSKU":                        getIsSgxEnabledSKU,
		"getShouldConfigureHTTPProxy":               getShouldConfigureHTTPProxy,
		"getShouldConfigureHTTPProxyCA":             getShouldConfigureHTTPProxyCA,
		"getAzureEnvironmentFilepath":               getAzureEnvironmentFilepath,
		"getLinuxAdminUsername":                     getLinuxAdminUsername,
		"getTargetEnvironment":                      getTargetEnvironment,
		"getTargetCloud":                            getTargetCloud,
	}
}

func getFuncMapForContainerdConfigTemplate() template.FuncMap {
	return template.FuncMap{
		"derefBool":  deref[bool],
		"getGpuNode": getGpuNode,
	}
}

func getStringFromVMType(enum nbcontractv1.ClusterConfig_VM) string {
	switch enum {
	case nbcontractv1.ClusterConfig_STANDARD:
		return vmTypeStandard
	case nbcontractv1.ClusterConfig_VMSS:
		return vmTypeVmss
	default:
		return ""
	}
}

func getStringFromNetworkPluginType(enum nbcontractv1.NetworkPlugin) string {
	switch enum {
	case nbcontractv1.NetworkPlugin_NP_AZURE:
		return networkPluginAzure
	case nbcontractv1.NetworkPlugin_NP_KUBENET:
		return networkPluginkubenet
	default:
		return ""
	}
}

func getStringFromNetworkPolicyType(enum nbcontractv1.NetworkPolicy) string {
	switch enum {
	case nbcontractv1.NetworkPolicy_NPO_AZURE:
		return networkPolicyAzure
	case nbcontractv1.NetworkPolicy_NPO_CALICO:
		return networkPolicyCalico
	default:
		return ""
	}
}

func getStringFromLoadBalancerSkuType(enum nbcontractv1.LoadBalancerConfig_LoadBalancerSku) string {
	switch enum {
	case nbcontractv1.LoadBalancerConfig_BASIC:
		return loadBalancerBasic
	case nbcontractv1.LoadBalancerConfig_STANDARD:
		return loadBalancerStandard
	default:
		return ""
	}
}

// deref is a helper function to dereference a pointer of any type to its value
func deref[T interface{}](p *T) T {
	if p == nil {
		var zeroValue T
		return zeroValue
	}
	return *p
}

func getStringifiedStringArray(arr []string, delimiter string) string {
	if len(arr) == 0 {
		return ""
	}

	return strings.Join(arr, delimiter)
}

// getKubenetTemplate returns the base64 encoded Kubenet template.
func getKubenetTemplate() string {
	return base64.StdEncoding.EncodeToString(kubenetTemplateContent)
}

func getContainerdConfig(nbcontract *nbcontractv1.Configuration) string {
	if nbcontract == nil {
		return ""
	}

	containerdConfig, err := containerdConfigFromNodeBootstrapContract(nbcontract)
	if err != nil {
		return fmt.Sprintf("error getting containerd config from node bootstrap variables: %v", err)
	}

	return base64.StdEncoding.EncodeToString([]byte(containerdConfig))
}

func containerdConfigFromNodeBootstrapContract(nbcontract *nbcontractv1.Configuration) (string, error) {
	if nbcontract == nil {
		return "", fmt.Errorf("node bootstrap contract is nil")
	}

	var buffer bytes.Buffer
	if err := containerdConfigTemplateForNBContract.Execute(&buffer, nbcontract); err != nil {
		return "", fmt.Errorf("error executing containerd config template for NBContract: %w", err)
	}

	return buffer.String(), nil
}

func getIsMIGNode(gpuInstanceProfile string) bool {
	return gpuInstanceProfile != ""
}

func getCustomCACertsStatus(customCACerts []string) bool {
	if len(customCACerts) > 0 {
		return true
	}
	return false
}

func getEnableTLSBootstrap(bootstrapConfig *nbcontractv1.TLSBootstrappingConfig) bool {
	return bootstrapConfig.GetTlsBootstrappingToken() != ""
}

func getEnableSecureTLSBootstrap(bootstrapConfig *nbcontractv1.TLSBootstrappingConfig) bool {
	// TODO: Change logic to default to false once Secure TLS Bootstrapping is complete
	return bootstrapConfig.GetEnableSecureTlsBootstrapping()
}

func getTLSBootstrapToken(bootstrapConfig *nbcontractv1.TLSBootstrappingConfig) string {
	return bootstrapConfig.GetTlsBootstrappingToken()
}

func getCustomSecureTLSBootstrapAADServerAppID(bootstrapConfig *nbcontractv1.TLSBootstrappingConfig) string {
	return bootstrapConfig.GetCustomSecureTlsBootstrappingAppserverAppid()
}

func getIsKrustlet(wr nbcontractv1.WorkloadRuntime) bool {
	return wr == nbcontractv1.WorkloadRuntime_WASM_WASI
}

func getEnsureNoDupePromiscuousBridge(nc *nbcontractv1.NetworkConfig) bool {
	return nc.GetNetworkPlugin() == nbcontractv1.NetworkPlugin_NP_KUBENET && nc.GetNetworkPolicy() != nbcontractv1.NetworkPolicy_NPO_CALICO
}

func getHasSearchDomain(csd *nbcontractv1.CustomSearchDomain) bool {
	if csd.GetCustomSearchDomainName() != "" && csd.GetCustomSearchDomainRealmUser() != "" && csd.GetCustomSearchDomainRealmPassword() != "" {
		return true
	}
	return false
}

func getCSEHelpersFilepath() string {
	return cseHelpersScriptFilepath
}

func getCSEDistroHelpersFilepath() string {
	return cseHelpersScriptDistroFilepath
}

func getCSEInstallFilepath() string {
	return cseInstallScriptFilepath
}

func getCSEDistroInstallFilepath() string {
	return cseInstallScriptDistroFilepath
}

func getCSEConfigFilepath() string {
	return cseConfigScriptFilepath
}

func getCustomSearchDomainFilepath() string {
	return customSearchDomainsCSEScriptFilepath
}

func getDHCPV6ServiceFilepath() string {
	return dhcpV6ServiceCSEScriptFilepath
}

func getDHCPV6ConfigFilepath() string {
	return dhcpV6ConfigCSEScriptFilepath
}

// getSysctlContent converts nbcontractv1.SysctlConfig to a string with key=value pairs, with default values.
//
//gocyclo:ignore
func getSysctlContent(s *nbcontractv1.SysctlConfig) string {
	// This is a partial workaround to this upstream Kubernetes issue:
	// https://github.com/kubernetes/kubernetes/issues/41916#issuecomment-312428731

	if s == nil {
		// If the sysctl config is nil, setting it to non-nil so that it can go through the defaulting logic below to get the default values.
		s = &nbcontractv1.SysctlConfig{}
	}

	m := make(map[string]int32)
	m["net.ipv4.tcp_retries2"] = 8
	m["net.core.message_burst"] = 80
	m["net.core.message_cost"] = 40

	// Access the variable directly, instead of using the getter, so that it knows whether it's nil or not.
	// This is based on protobuf3 explicit presence feature.
	// Other directly access variables in this function implies the same idea.
	if s.NetCoreSomaxconn == nil {
		m["net.core.somaxconn"] = 16384
	} else {
		// either using getter for NetCoreSomaxconn or direct access is fine because we ensure it's not nil.
		m["net.core.somaxconn"] = s.GetNetCoreSomaxconn()
	}

	if s.NetIpv4TcpMaxSynBacklog == nil {
		m["net.ipv4.tcp_max_syn_backlog"] = 16384
	} else {
		m["net.ipv4.tcp_max_syn_backlog"] = s.GetNetIpv4TcpMaxSynBacklog()
	}

	if s.NetIpv4NeighDefaultGcThresh1 == nil {
		m["net.ipv4.neigh.default.gc_thresh1"] = 4096
	} else {
		m["net.ipv4.neigh.default.gc_thresh1"] = s.GetNetIpv4NeighDefaultGcThresh1()
	}

	if s.NetIpv4NeighDefaultGcThresh2 == nil {
		m["net.ipv4.neigh.default.gc_thresh2"] = 8192
	} else {
		m["net.ipv4.neigh.default.gc_thresh2"] = s.GetNetIpv4NeighDefaultGcThresh2()
	}

	if s.NetIpv4NeighDefaultGcThresh3 == nil {
		m["net.ipv4.neigh.default.gc_thresh3"] = 16384
	} else {
		m["net.ipv4.neigh.default.gc_thresh3"] = s.GetNetIpv4NeighDefaultGcThresh3()
	}

	if s.NetCoreNetdevMaxBacklog != nil {
		m["net.core.netdev_max_backlog"] = s.GetNetCoreNetdevMaxBacklog()
	}

	if s.NetCoreRmemDefault != nil {
		m["net.core.rmem_default"] = s.GetNetCoreRmemDefault()
	}

	if s.NetCoreRmemMax != nil {
		m["net.core.rmem_max"] = s.GetNetCoreRmemMax()
	}

	if s.NetCoreWmemDefault != nil {
		m["net.core.wmem_default"] = s.GetNetCoreWmemDefault()
	}

	if s.NetCoreWmemMax != nil {
		m["net.core.wmem_max"] = s.GetNetCoreWmemMax()
	}

	if s.NetCoreOptmemMax != nil {
		m["net.core.optmem_max"] = s.GetNetCoreOptmemMax()
	}

	if s.NetIpv4TcpMaxTwBuckets != nil {
		m["net.ipv4.tcp_max_tw_buckets"] = s.GetNetIpv4TcpMaxTwBuckets()
	}

	if s.NetIpv4TcpFinTimeout != nil {
		m["net.ipv4.tcp_fin_timeout"] = s.GetNetIpv4TcpFinTimeout()
	}

	if s.NetIpv4TcpKeepaliveTime != nil {
		m["net.ipv4.tcp_keepalive_time"] = s.GetNetIpv4TcpKeepaliveTime()
	}

	if s.NetIpv4TcpKeepaliveProbes != nil {
		m["net.ipv4.tcp_keepalive_probes"] = s.GetNetIpv4TcpKeepaliveProbes()
	}

	if s.NetIpv4TcpkeepaliveIntvl != nil {
		m["net.ipv4.tcp_keepalive_intvl"] = s.GetNetIpv4TcpkeepaliveIntvl()
	}

	if s.NetIpv4TcpTwReuse != nil {
		if s.GetNetIpv4TcpTwReuse() {
			m["net.ipv4.tcp_tw_reuse"] = 1
		} else {
			m["net.ipv4.tcp_tw_reuse"] = 0
		}
	}

	if s.GetNetIpv4IpLocalPortRange() != "" {
		if getPortRangeEndValue(s.GetNetIpv4IpLocalPortRange()) > 65330 {
			m["net.ipv4.ip_local_reserved_ports"] = 65330
		}
	}

	if s.NetNetfilterNfConntrackMax != nil {
		m["net.netfilter.nf_conntrack_max"] = s.GetNetNetfilterNfConntrackMax()
	}

	if s.NetNetfilterNfConntrackBuckets != nil {
		m["net.netfilter.nf_conntrack_buckets"] = s.GetNetNetfilterNfConntrackBuckets()
	}

	if s.FsInotifyMaxUserWatches != nil {
		m["fs.inotify.max_user_watches"] = s.GetFsInotifyMaxUserWatches()
	}

	if s.FsFileMax != nil {
		m["fs.file-max"] = s.GetFsFileMax()
	}

	if s.FsAioMaxNr != nil {
		m["fs.aio-max-nr"] = s.GetFsAioMaxNr()
	}

	if s.FsNrOpen != nil {
		m["fs.nr_open"] = s.GetFsNrOpen()
	}

	if s.KernelThreadsMax != nil {
		m["kernel.threads-max"] = s.GetKernelThreadsMax()
	}

	if s.VMMaxMapCount != nil {
		m["vm.max_map_count"] = s.GetVMMaxMapCount()
	}

	if s.VMSwappiness != nil {
		m["vm.swappiness"] = s.GetVMSwappiness()
	}

	if s.VMVfsCachePressure != nil {
		m["vm.vfs_cache_pressure"] = s.GetVMVfsCachePressure()
	}

	return base64.StdEncoding.EncodeToString([]byte(createSortedKeyValuePairs(m, " ")))
}

func getShouldConfigContainerdUlimits(u *nbcontractv1.UlimitConfig) bool {
	return u != nil
}

// getUlimitContent converts nbcontractv1.UlimitConfig to a string with key=value pairs.
func getUlimitContent(u *nbcontractv1.UlimitConfig) string {
	if u == nil {
		return ""
	}

	header := "[Service]\n"
	m := make(map[string]string)
	if u.NoFile != nil {
		m["LimitNOFILE"] = u.GetNoFile()
	}

	if u.MaxLockedMemory != nil {
		m["LimitMEMLOCK"] = u.GetMaxLockedMemory()
	}

	return base64.StdEncoding.EncodeToString([]byte(header + createSortedKeyValuePairs(m, " ")))
}

// getPortRangeEndValue returns the end value of the port range where the input is in the format of "start end".
func getPortRangeEndValue(portRange string) int {
	if portRange == "" {
		return -1
	}

	arr := strings.Split(portRange, " ")

	// we are expecting only two values, start and end.
	if len(arr) != 2 {
		return -1
	}

	start, end := int(0), int(0)
	var err error

	// the start value should be a valid port number.
	if start, err = strconv.Atoi(arr[0]); err != nil {
		log.Printf("error converting port range start value to int: %v", err)
		return -1
	}

	// the end value should be a valid port number.
	if end, err = strconv.Atoi(arr[1]); err != nil {
		log.Printf("error converting port range end value to int: %v", err)
		return -1
	}

	if start <= 0 || end <= 0 {
		log.Printf("port range values should be greater than 0: %d", start)
		return -1
	}

	if start >= end {
		log.Printf("port range end value should be greater than the start value: %d >= %d", start, end)
		return -1
	}

	return end
}

// createSortedKeyValuePairs creates a string with key=value pairs, sorted by key, with custom delimiter.
func createSortedKeyValuePairs[T any](m map[string]T, delimiter string) string {
	keys := []string{}
	for key := range m {
		keys = append(keys, key)
	}

	// we are sorting the keys for deterministic output for readability and testing.
	sort.Strings(keys)
	var buf bytes.Buffer
	i := 0
	for _, key := range keys {
		i++
		// set the last delimiter to empty string
		if i == len(keys) {
			delimiter = ""
		}
		buf.WriteString(fmt.Sprintf("%s=%v%s", key, m[key], delimiter))
	}
	return buf.String()
}

// getKubeletConfigFileEnabled returns true if the kubelet config content is not empty and the k8s version is greater than or equal to 1.14.0.
func getKubeletConfigFileEnabled(configContent string, k8sVersion string) bool {
	// In AgentBaker's utils.go, it also checks if the orchestrator is Kubernetes. We assume it is always Kubernetes here.
	return configContent != "" && IsKubernetesVersionGe(k8sVersion, "1.14.0")
}

// IsKubernetesVersionGe returns true if actualVersion is greater than or equal to version.
func IsKubernetesVersionGe(actualVersion, version string) bool {
	v1, _ := semver.Make(actualVersion)
	v2, _ := semver.Make(version)
	return v1.GE(v2)
}

func getExcludeMasterFromStandardLB(lb *nbcontractv1.LoadBalancerConfig) bool {
	if lb == nil || lb.ExcludeMasterFromStandardLoadBalancer == nil {
		return true
	}
	return lb.GetExcludeMasterFromStandardLoadBalancer()
}

func getMaxLBRuleCount(lb *nbcontractv1.LoadBalancerConfig) int32 {
	if lb == nil || lb.MaxLoadBalancerRuleCount == nil {
		return int32(148)
	}
	return lb.GetMaxLoadBalancerRuleCount()
}

func getGpuNode(vmSize string) bool {
	return utils.IsNvidiaEnabledSKU(vmSize)
}

func getGpuImageSha(vmSize string) string {
	return utils.GetAKSGPUImageSHA(vmSize)
}

func getGpuDriverVersion(vmSize string) string {
	return utils.GetGPUDriverVersion(vmSize)
}

// IsSgxEnabledSKU determines if an VM SKU has SGX driver support.
func getIsSgxEnabledSKU(vmSize string) bool {
	switch vmSize {
	case vmSizeStandardDc2s, vmSizeStandardDc4s:
		return true
	}
	return false
}

func getShouldConfigureHTTPProxy(httpProxyConfig *nbcontractv1.HTTPProxyConfig) bool {
	return httpProxyConfig.GetHttpProxy() != "" || httpProxyConfig.GetHttpsProxy() != ""
}

func getShouldConfigureHTTPProxyCA(httpProxyConfig *nbcontractv1.HTTPProxyConfig) bool {
	return httpProxyConfig.GetProxyTrustedCa() != ""
}

func getAzureEnvironmentFilepath(v *nbcontractv1.CustomCloudConfig) string {
	if v.GetIsAksCustomCloud() {
		return fmt.Sprintf("/etc/kubernetes/%s.json", v.GetTargetEnvironment())
	}
	return ""
}

func getLinuxAdminUsername(username string) string {
	if username == "" {
		return defaultLinuxUser
	}
	return username
}

func getTargetEnvironment(v *nbcontractv1.CustomCloudConfig) string {
	if v.GetTargetEnvironment() == "" {
		return defaultCloudName
	}

	return v.GetTargetEnvironment()
}

func getTargetCloud(v *nbcontractv1.AuthConfig) string {
	if v.GetTargetCloud() == "" {
		return defaultCloudName
	}

	return v.GetTargetCloud()
}
