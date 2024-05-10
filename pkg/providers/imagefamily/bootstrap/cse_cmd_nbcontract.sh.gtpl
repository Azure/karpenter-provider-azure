#!/bin/bash

set -o allexport # export all variables to subshells
echo '#EOF' >> /opt/azure/manifest.json # wait_for_file looks for this
mkdir -p /var/log/azure/Microsoft.Azure.Extensions.CustomScript/events # expected, but not created w/o CSE

echo $(date),$(hostname) > /var/log/azure/cluster-provision-cse-output.log;
for i in $(seq 1 1200); do
grep -Fq "EOF" /opt/azure/containers/provision.sh && break;
if [ $i -eq 1200 ]; then exit 100; else sleep 1; fi;
done;
{{if getIsAksCustomCloud .CustomCloudConfig}}
for i in $(seq 1 1200); do
grep -Fq "EOF" {{getInitAKSCustomCloudFilepath}} && break;
if [ $i -eq 1200 ]; then exit 100; else sleep 1; fi;
done;
REPO_DEPOT_ENDPOINT="{{.CustomCloudConfig.GetRepoDepotEndpoint}}"
{{getInitAKSCustomCloudFilepath}} >> /var/log/azure/cluster-provision.log 2>&1;
{{end}}
ADMINUSER={{getLinuxAdminUsername .GetLinuxAdminUsername}}
MOBY_VERSION=
TENANT_ID={{.AuthConfig.GetTenantId}}
KUBERNETES_VERSION={{.GetKubernetesVersion}}
KUBE_BINARY_URL={{.KubeBinaryConfig.GetKubeBinaryUrl}}
CUSTOM_KUBE_BINARY_URL={{.KubeBinaryConfig.GetCustomKubeBinaryUrl}}
PRIVATE_KUBE_BINARY_URL="{{.KubeBinaryConfig.GetPrivateKubeBinaryUrl}}"
KUBEPROXY_URL={{.GetKubeProxyUrl}}
APISERVER_PUBLIC_KEY={{.ApiServerConfig.GetApiServerPublicKey}}
SUBSCRIPTION_ID={{.AuthConfig.GetSubscriptionId}}
RESOURCE_GROUP={{.ClusterConfig.GetResourceGroup}}
LOCATION={{.ClusterConfig.GetLocation}}
VM_TYPE={{getStringFromVMType .ClusterConfig.GetVmType}}
SUBNET={{.ClusterConfig.GetClusterNetworkConfig.GetSubnet}}
NETWORK_SECURITY_GROUP={{.ClusterConfig.GetClusterNetworkConfig.GetSecurityGroupName}}
VIRTUAL_NETWORK={{.ClusterConfig.GetClusterNetworkConfig.GetVnetName}}
VIRTUAL_NETWORK_RESOURCE_GROUP={{.ClusterConfig.GetClusterNetworkConfig.GetVnetResourceGroup}}
ROUTE_TABLE={{.ClusterConfig.GetClusterNetworkConfig.GetRouteTable}}
PRIMARY_AVAILABILITY_SET={{.ClusterConfig.GetPrimaryAvailabilitySet}}
PRIMARY_SCALE_SET={{.ClusterConfig.GetPrimaryScaleSet}}
SERVICE_PRINCIPAL_CLIENT_ID={{.AuthConfig.GetServicePrincipalId}}
NETWORK_PLUGIN={{getStringFromNetworkPluginType .GetNetworkConfig.GetNetworkPlugin}}
NETWORK_POLICY="{{getStringFromNetworkPolicyType .GetNetworkConfig.GetNetworkPolicy}}"
VNET_CNI_PLUGINS_URL={{.GetNetworkConfig.GetVnetCniPluginsUrl}}
CNI_PLUGINS_URL={{.GetNetworkConfig.GetCniPluginsUrl}}
CLOUDPROVIDER_BACKOFF=true
CLOUDPROVIDER_BACKOFF_MODE=v2
CLOUDPROVIDER_BACKOFF_RETRIES=6
CLOUDPROVIDER_BACKOFF_EXPONENT=0
CLOUDPROVIDER_BACKOFF_DURATION=5
CLOUDPROVIDER_BACKOFF_JITTER=0
CLOUDPROVIDER_RATELIMIT=true
CLOUDPROVIDER_RATELIMIT_QPS=10
CLOUDPROVIDER_RATELIMIT_QPS_WRITE=10
CLOUDPROVIDER_RATELIMIT_BUCKET=100
CLOUDPROVIDER_RATELIMIT_BUCKET_WRITE=100
LOAD_BALANCER_DISABLE_OUTBOUND_SNAT={{.ClusterConfig.GetLoadBalancerConfig.GetDisableOutboundSnat}}
USE_MANAGED_IDENTITY_EXTENSION={{.AuthConfig.GetUseManagedIdentityExtension}}
USE_INSTANCE_METADATA={{.ClusterConfig.GetUseInstanceMetadata}}
LOAD_BALANCER_SKU={{getStringFromLoadBalancerSkuType .ClusterConfig.GetLoadBalancerConfig.GetLoadBalancerSku}}
EXCLUDE_MASTER_FROM_STANDARD_LB={{getExcludeMasterFromStandardLB .ClusterConfig.GetLoadBalancerConfig}}
MAXIMUM_LOADBALANCER_RULE_COUNT={{getMaxLBRuleCount .ClusterConfig.GetLoadBalancerConfig}}
CONTAINER_RUNTIME=containerd
CLI_TOOL=ctr
CONTAINERD_DOWNLOAD_URL_BASE={{.ContainerdConfig.GetContainerdDownloadUrlBase}} 
NETWORK_MODE="transparent"
KUBE_BINARY_URL={{.KubeBinaryConfig.GetKubeBinaryUrl}}
USER_ASSIGNED_IDENTITY_ID={{.AuthConfig.GetAssignedIdentityId}}
API_SERVER_NAME={{.ApiServerConfig.GetApiServerName}}
IS_VHD={{getIsVHD .IsVhd}}
GPU_NODE={{getEnableNvidia .}}
SGX_NODE={{getIsSgxEnabledSKU .VmSize}}
MIG_NODE={{getIsMIGNode .GpuConfig.GetGpuInstanceProfile}}
CONFIG_GPU_DRIVER_IF_NEEDED={{.GpuConfig.GetConfigGpuDriver}}
ENABLE_GPU_DEVICE_PLUGIN_IF_NEEDED={{.GpuConfig.GetGpuDevicePlugin}}
TELEPORTD_PLUGIN_DOWNLOAD_URL={{.TeleportConfig.GetTeleportdPluginDownloadUrl}}
CREDENTIAL_PROVIDER_DOWNLOAD_URL={{.KubeBinaryConfig.GetLinuxCredentialProviderUrl}}
CONTAINERD_VERSION={{.ContainerdConfig.GetContainerdVersion}}
CONTAINERD_PACKAGE_URL={{.ContainerdConfig.GetContainerdPackageUrl}}
RUNC_VERSION={{.RuncConfig.GetRuncVersion}}
RUNC_PACKAGE_URL={{.RuncConfig.GetRuncPackageUrl}}
ENABLE_HOSTS_CONFIG_AGENT="{{.GetEnableHostsConfigAgent}}"
DISABLE_SSH="{{getDisableSSH .}}"
NEEDS_CONTAINERD="true"
TELEPORT_ENABLED="{{.TeleportConfig.GetStatus}}"
SHOULD_CONFIGURE_HTTP_PROXY="{{getShouldConfigureHTTPProxy .HttpProxyConfig}}"
SHOULD_CONFIGURE_HTTP_PROXY_CA="{{getShouldConfigureHTTPProxyCA .HttpProxyConfig}}"
HTTP_PROXY_TRUSTED_CA="{{.HttpProxyConfig.GetProxyTrustedCa}}"
SHOULD_CONFIGURE_CUSTOM_CA_TRUST="{{getCustomCACertsStatus .GetCustomCaCerts}}"
CUSTOM_CA_TRUST_COUNT="{{len .GetCustomCaCerts}}"
{{range $i, $cert := .CustomCaCerts}}
CUSTOM_CA_CERT_{{$i}}="{{$cert}}"
{{end}}
IS_KRUSTLET="{{getIsKrustlet .GetWorkloadRuntime}}"
GPU_NEEDS_FABRIC_MANAGER="{{getGPUNeedsFabricManager .VmSize}}"
NEEDS_DOCKER_LOGIN="false"
IPV6_DUAL_STACK_ENABLED="{{.GetIpv6DualStackEnabled}}"
OUTBOUND_COMMAND="{{.GetOutboundCommand}}"
ENABLE_UNATTENDED_UPGRADES="{{.GetEnableUnattendedUpgrade}}"
ENSURE_NO_DUPE_PROMISCUOUS_BRIDGE="{{getEnsureNoDupePromiscuousBridge .GetNetworkConfig}}"
SHOULD_CONFIG_SWAP_FILE="{{getEnableSwapConfig .CustomLinuxOsConfig}}"
SHOULD_CONFIG_TRANSPARENT_HUGE_PAGE="{{getShouldCOnfigTransparentHugePage .CustomLinuxOsConfig}}"
SHOULD_CONFIG_CONTAINERD_ULIMITS="{{getShouldConfigContainerdUlimits .CustomLinuxOsConfig.GetUlimitConfig}}"
CONTAINERD_ULIMITS="{{getUlimitContent .CustomLinuxOsConfig.GetUlimitConfig}}"
{{/* both CLOUD and ENVIRONMENT have special values when IsAKSCustomCloud == true */}}
{{/* CLOUD uses AzureStackCloud and seems to be used by kubelet, k8s cloud provider */}}
{{/* target environment seems to go to ARM SDK config */}}
{{/* not sure why separate/inconsistent? */}}
{{/* see GetCustomEnvironmentJSON for more weirdness. */}}
TARGET_CLOUD="{{getTargetCloud .}}"
TARGET_ENVIRONMENT="{{getTargetEnvironment .}}"
CUSTOM_ENV_JSON="{{.CustomCloudConfig.GetCustomEnvJsonContent}}"
IS_CUSTOM_CLOUD="{{getIsAksCustomCloud .CustomCloudConfig}}"
AKS_CUSTOM_CLOUD_CONTAINER_REGISTRY_DNS_SUFFIX="{{- if getIsAksCustomCloud .CustomCloudConfig}}{{.CustomCloudConfig.GetContainerRegistryDnsSuffix}}{{end}}"
CSE_HELPERS_FILEPATH="{{getCSEHelpersFilepath}}"
CSE_DISTRO_HELPERS_FILEPATH="{{getCSEDistroHelpersFilepath}}"
CSE_INSTALL_FILEPATH="{{getCSEInstallFilepath}}"
CSE_DISTRO_INSTALL_FILEPATH="{{getCSEDistroInstallFilepath}}"
CSE_CONFIG_FILEPATH="{{getCSEConfigFilepath}}"
AZURE_PRIVATE_REGISTRY_SERVER="{{.GetAzurePrivateRegistryServer}}"
HAS_CUSTOM_SEARCH_DOMAIN="{{getHasSearchDomain .GetCustomSearchDomainConfig}}"
CUSTOM_SEARCH_DOMAIN_FILEPATH="{{getCustomSearchDomainFilepath}}"
HTTP_PROXY_URLS="{{.HttpProxyConfig.GetHttpProxy}}"
HTTPS_PROXY_URLS="{{.HttpProxyConfig.GetHttpsProxy}}"
NO_PROXY_URLS="{{getStringifiedStringArray .HttpProxyConfig.GetNoProxyEntries ","}}"
PROXY_VARS="{{getProxyVariables .HttpProxyConfig}}"
ENABLE_TLS_BOOTSTRAPPING="{{getEnableTLSBootstrap .TlsBootstrappingConfig}}"
ENABLE_SECURE_TLS_BOOTSTRAPPING="{{getEnableSecureTLSBootstrap .TlsBootstrappingConfig}}"
CUSTOM_SECURE_TLS_BOOTSTRAP_AAD_SERVER_APP_ID="{{getCustomSecureTLSBootstrapAADServerAppID .TlsBootstrappingConfig}}"
DHCPV6_SERVICE_FILEPATH="{{getDHCPV6ServiceFilepath}}"
DHCPV6_CONFIG_FILEPATH="{{getDHCPV6ConfigFilepath}}"
THP_ENABLED="{{.CustomLinuxOsConfig.GetTransparentHugepageSupport}}"
THP_DEFRAG="{{.CustomLinuxOsConfig.GetTransparentDefrag}}"
SERVICE_PRINCIPAL_FILE_CONTENT="{{getServicePrincipalFileContent .AuthConfig}}"
KUBELET_CLIENT_CONTENT="{{.KubeletConfig.GetKubeletClientKey}}"
KUBELET_CLIENT_CERT_CONTENT="{{.KubeletConfig.GetKubeletClientCertContent}}"
KUBELET_CONFIG_FILE_ENABLED="{{.KubeletConfig.GetEnableKubeletConfigFile}}"
KUBELET_CONFIG_FILE_CONTENT="{{.KubeletConfig.GetKubeletConfigFileContent}}"
SWAP_FILE_SIZE_MB="{{.CustomLinuxOsConfig.GetSwapFileSize}}"
GPU_DRIVER_VERSION="{{getGpuDriverVersion .VmSize}}"
GPU_IMAGE_SHA="{{getGpuImageSha .VmSize}}"
GPU_INSTANCE_PROFILE="{{.GpuConfig.GetGpuInstanceProfile}}"
CUSTOM_SEARCH_DOMAIN_NAME="{{.CustomSearchDomainConfig.GetDomainName}}"
CUSTOM_SEARCH_REALM_USER="{{.CustomSearchDomainConfig.GetRealmUser}}"
CUSTOM_SEARCH_REALM_PASSWORD="{{.CustomSearchDomainConfig.GetRealmPassword}}"
MESSAGE_OF_THE_DAY="{{.GetMessageOfTheDay}}"
HAS_KUBELET_DISK_TYPE="{{getHasKubeletDiskType .KubeletConfig}}"
NEEDS_CGROUPV2="{{.GetNeedsCgroupv2}}"
TLS_BOOTSTRAP_TOKEN="{{getTLSBootstrapToken .TlsBootstrappingConfig}}"
KUBELET_FLAGS="{{createSortedKeyValueStringPairs .KubeletConfig.GetKubeletFlags " "}}"
NETWORK_POLICY="{{getStringFromNetworkPolicyType .NetworkConfig.GetNetworkPolicy}}"
KUBELET_NODE_LABELS="{{createSortedKeyValueStringPairs .KubeletConfig.GetKubeletNodeLabels ","}}"
AZURE_ENVIRONMENT_FILEPATH="{{getAzureEnvironmentFilepath .}}"
KUBE_CA_CRT="{{.GetKubernetesCaCert}}"
KUBENET_TEMPLATE="{{getKubenetTemplate}}"
CONTAINERD_CONFIG_CONTENT="{{getContainerdConfig .}}"
IS_KATA="{{.GetIsKata}}"
ARTIFACT_STREAMING_ENABLED="{{.GetEnableArtifactStreaming}}"
SYSCTL_CONTENT="{{getSysctlContent .CustomLinuxOsConfig.GetSysctlConfig}}"
PRIVATE_EGRESS_PROXY_ADDRESS=""
/usr/bin/nohup /bin/bash -c "/bin/bash /opt/azure/containers/provision_start.sh"