#!/bin/bash

set -o allexport # export all variables to subshells
echo '#EOF' >> /opt/azure/manifest.json # wait_for_file looks for this
mkdir -p /var/log/azure/Microsoft.Azure.Extensions.CustomScript/events # expected, but not created w/o CSE

echo $(date),$(hostname) > /var/log/azure/cluster-provision-cse-output.log;
for i in $(seq 1 1200); do
grep -Fq "EOF" /opt/azure/containers/provision.sh && break;
if [ $i -eq 1200 ]; then exit 100; else sleep 1; fi;
done;
{{if getBoolFromFeatureState .CustomCloudConfig.Status}}
for i in $(seq 1 1200); do
grep -Fq "EOF" {{.CustomCloudConfig.InitFilePath}} && break;
if [ $i -eq 1200 ]; then exit 100; else sleep 1; fi;
done;
REPO_DEPOT_ENDPOINT="{{.CustomCloudConfig.RepoDepotEndpoint}}"
{{.CustomCloudConfig.InitFilePath}} >> /var/log/azure/cluster-provision.log 2>&1;
{{end}}
ADMINUSER={{.LinuxAdminUsername}}
KUBERNETES_VERSION={{.KubernetesVersion}}
KUBE_BINARY_URL={{.KubeBinaryConfig.GetKubeBinaryUrl}}
CUSTOM_KUBE_BINARY_URL={{.KubeBinaryConfig.GetCustomKubeBinaryUrl}}
PRIVATE_KUBE_BINARY_URL={{.KubeBinaryConfig.GetPrivateKubeBinaryUrl}}
KUBEPROXY_URL={{.KubeproxyUrl}}
API_SERVER_NAME={{.ApiserverConfig.ApiserverName}}
APISERVER_PUBLIC_KEY={{.ApiserverConfig.ApiserverPublicKey}}
TENANT_ID={{.AuthConfig.GetTenantId}}
TARGET_CLOUD="{{.AuthConfig.GetTargetCloud}}"
SUBSCRIPTION_ID={{.AuthConfig.GetSubscriptionId}}
RESOURCE_GROUP={{.ClusterConfig.GetResourceGroup}}
LOCATION={{.ClusterConfig.GetLocation}}
VM_TYPE={{getStringFromVmType .ClusterConfig.GetVmType}}
PRIMARY_AVAILABILITY_SET={{.ClusterConfig.GetPrimaryAvailabilitySet}}
PRIMARY_SCALE_SET={{.ClusterConfig.GetPrimaryScaleSet}}
SERVICE_PRINCIPAL_CLIENT_ID="{{.AuthConfig.GetServicePrincipalId}}"
SERVICE_PRINCIPAL_FILE_CONTENT="{{.AuthConfig.GetServicePrincipalSecret}}"
USER_ASSIGNED_IDENTITY_ID="{{.AuthConfig.GetAssignedIdentityId}}"
USE_MANAGED_IDENTITY_EXTENSION={{.AuthConfig.GetUseManagedIdentityExtension}}
NETWORK_MODE={{getStringFromNetworkModeType .NetworkConfig.NetworkMode}}
NETWORK_PLUGIN={{getStringFromNetworkPluginType .NetworkConfig.NetworkPlugin}}
NETWORK_POLICY="{{getStringFromNetworkPolicyType .NetworkConfig.NetworkPolicy}}"
VNET_CNI_PLUGINS_URL={{.NetworkConfig.VnetCniPluginsUrl}}
CNI_PLUGINS_URL={{.NetworkConfig.CniPluginsUrl}}
NETWORK_SECURITY_GROUP={{.ClusterConfig.GetVirtualNetworkConfig.GetSecurityGroupName}}
VIRTUAL_NETWORK={{.ClusterConfig.GetVirtualNetworkConfig.GetVnetName}}
VIRTUAL_NETWORK_RESOURCE_GROUP={{.ClusterConfig.GetVirtualNetworkConfig.GetVnetResourceGroup}}
SUBNET={{.ClusterConfig.GetClusterVirtualNetworkConfig.GetSubnet}}
ROUTE_TABLE={{.ClusterConfig.GetClusterVirtualNetworkConfig.GetRouteTable}}
USE_INSTANCE_METADATA={{.ClusterConfig.GetUseInstanceMetadata}}
LOAD_BALANCER_SKU={{getStringFromLoadBalancerSkuType .ClusterConfig.GetLoadBalancerConfig.GetLoadBalancerSku}}
EXCLUDE_MASTER_FROM_STANDARD_LB={{getExcludeMasterFromStandardLB .LoadBalancerConfig}}
MAXIMUM_LOADBALANCER_RULE_COUNT={{getMaxLBRuleCount .LoadBalancerConfig}}
CONTAINERD_DOWNLOAD_URL_BASE={{.ContainerdConfig.GetContainerdDownloadUrlBase}}
CONTAINERD_VERSION={{.ContainerdConfig.GetContainerdVersion}}
CONTAINERD_PACKAGE_URL={{.ContainerdConfig.GetContainerdPackageUrl}}
CONTAINERD_CONFIG_CONTENT="{{getContainerdConfig .}}"
IS_VHD={{.IsVhd}}
GPU_NODE={{getGpuNode .VmSize}}
GPU_IMAGE_SHA="{{getGpuImageSha .VmSize}}"
GPU_INSTANCE_PROFILE="{{.GpuConfig.GpuInstanceProfile}}"
CONFIG_GPU_DRIVER_IF_NEEDED={{getBoolFromFeatureState .GpuConfig.GetConfigGpuDriver}}
ENABLE_GPU_DEVICE_PLUGIN_IF_NEEDED={{getBoolFromFeatureState .GpuConfig.GetGpuDevicePlugin}}
MIG_NODE="{{getIsMIGNode .GpuConfig.GetGpuInstanceProfile}}"
GPU_DRIVER_VERSION="{{getGpuDriverVersion .VmSize}}"
GPU_NEEDS_FABRIC_MANAGER="false"
SGX_NODE={{getIsSgxEnabledSKU .VmSize}}
TELEPORT_ENABLED="{{getBoolFromFeatureState .TeleportConfig.Status}}"
TELEPORTD_PLUGIN_DOWNLOAD_URL={{.TeleportConfig.TeleportdPluginDownloadUrl}}
RUNC_VERSION={{.RuncConfig.GetRuncVersion}}
RUNC_PACKAGE_URL={{.RuncConfig.GetRuncPackageUrl}}
ENABLE_HOSTS_CONFIG_AGENT="{{.GetEnableHostsConfigAgent}}"
DISABLE_SSH="{{not .GetEnableSsh}}"
SHOULD_CONFIGURE_HTTP_PROXY="{{getShouldConfigureHttpProxy .HttpProxyConfig}}"
SHOULD_CONFIGURE_HTTP_PROXY_CA="{{getShouldConfigureHttpProxyCa .HttpProxyConfig}}"
HTTP_PROXY_TRUSTED_CA="{{.HttpProxyConfig.GetProxyTrustedCa}}"
HTTP_PROXY_URLS="{{.HttpProxyConfig.GetHttpProxy}}"
HTTPS_PROXY_URLS="{{.HttpProxyConfig.GetHttpsProxy}}"
NO_PROXY_URLS="{{getStringifiedStringArray .HttpProxyConfig.GetNoProxyEntries ","}}"
SHOULD_CONFIGURE_CUSTOM_CA_TRUST="{{getCustomCACertsStatus .GetCustomCaCerts}}"
CUSTOM_CA_TRUST_COUNT="{{len .GetCustomCaCerts}}"
{{range $i, $cert := .CustomCaCerts}}
CUSTOM_CA_CERT_{{$i}}="{{$cert}}"
{{end}}
IS_KRUSTLET="{{getIsKrustlet .GetWorkloadRuntime}}"
IPV6_DUAL_STACK_ENABLED="{{.GetIpv6DualStackEnabled}}"
ENABLE_UNATTENDED_UPGRADES={{.GetEnableUnattendedUpgrade}}
ENSURE_NO_DUPE_PROMISCUOUS_BRIDGE={{getEnsureNoDupePromiscuousBridge .GetNetworkConfig}}
SWAP_FILE_SIZE_MB="{{.CustomLinuxOsConfig.SwapFileSize}}"
TARGET_ENVIRONMENT="{{.CustomCloudConfig.TargetEnvironment}}"
CUSTOM_ENV_JSON="{{.CustomCloudConfig.CustomEnvJsonContent}}"
IS_CUSTOM_CLOUD="{{getBoolStringFromFeatureStatePtr .CustomCloudConfig.Status}}"
AZURE_PRIVATE_REGISTRY_SERVER="{{.AzurePrivateRegistryServer}}"
ENABLE_TLS_BOOTSTRAPPING="{{getEnableTLSBootstrap .TlsBootstrappingConfig}}"
ENABLE_SECURE_TLS_BOOTSTRAPPING="{{getEnableSecureTLSBootstrap .TlsBootstrappingConfig}}"
TLS_BOOTSTRAP_TOKEN="{{getTLSBootstrapToken .TlsBootstrappingConfig}}"
CUSTOM_SECURE_TLS_BOOTSTRAP_AAD_SERVER_APP_ID="{{getCustomSecureTLSBootstrapAADServerAppID .TlsBootstrappingConfig}}"
KUBELET_FLAGS="{{createSortedKeyValueStringPairs .KubeletConfig.GetKubeletFlags " "}}"
KUBELET_NODE_LABELS="{{createSortedKeyValueStringPairs .KubeletConfig.GetKubeletNodeLabels ","}}"
KUBELET_CLIENT_CONTENT="{{.KubeletConfig.GetKubeletClientKey}}"
KUBELET_CLIENT_CERT_CONTENT="{{.KubeletConfig.GetKubeletClientCertContent}}"
KUBELET_CONFIG_FILE_ENABLED="{{getKubeletConfigFileEnabled .KubeletConfig.GetKubeletConfigFileContent .GetKubernetesVersion}}"
KUBELET_CONFIG_FILE_CONTENT="{{.KubeletConfig.GetKubeletConfigFileContent}}"
CUSTOM_SEARCH_DOMAIN_NAME="{{.CustomSearchDomain.GetCustomSearchDomainName}}"
CUSTOM_SEARCH_REALM_USER="{{.CustomSearchDomain.GetCustomSearchDomainRealmUser}}"
CUSTOM_SEARCH_REALM_PASSWORD="{{.CustomSearchDomain.GetCustomSearchDomainRealmPassword}}"
HAS_CUSTOM_SEARCH_DOMAIN="{{getHasSearchDomain .GetCustomSearchDomain}}"
MESSAGE_OF_THE_DAY="{{.GetMessageOfTheDay}}"
THP_ENABLED="{{.CustomLinuxOsConfig.GetTransparentHugepageSupport}}"
THP_DEFRAG="{{.CustomLinuxOsConfig.GetTransparentDefrag}}"
SYSCTL_CONTENT="{{getSysctlContent .CustomLinuxOsConfig.GetSysctlConfig}}"
KUBE_CA_CRT="{{.ClusterCertificateAuthority}}"
KUBENET_TEMPLATE="{{getKubenetTemplate}}"
SHOULD_CONFIG_TRANSPARENT_HUGE_PAGE="false"
SHOULD_CONFIG_CONTAINERD_ULIMITS = {{getShouldConfigContainerdUlimits .CustomLinuxOsConfig.GetUlimitConfig}}
CONTAINERD_ULIMITS="{{getUlimitContent .CustomLinuxOsConfig.GetUlimitConfig}}"
OUTBOUND_COMMAND={{.GetOutboundCommand}}
IS_KATA="{{.GetIsKata}}"  # if we can get the value of distro of the VHD, we can compute this value in the Go binary on VHD
NEEDS_CGROUPV2="{{.GetNeedsCgroupv2}}" # if we can get the value of distro of the VHD, we can compute this value in the Go binary on VHD
SHOULD_CONFIG_SWAP_FILE="{{getShouldConfigSwapFile .CustomLinuxOsConfig.GetSwapFileSize}}"
HAS_KUBELET_DISK_TYPE="false" #Following Karpenter's default value. Set as "false" for now.
ARTIFACT_STREAMING_ENABLED="{{.GetEnableArtifactStreaming}}"
CSE_HELPERS_FILEPATH={{getCSEHelpersFilepath}}
CSE_DISTRO_HELPERS_FILEPATH={{getCSEDistroHelpersFilepath}}
CSE_INSTALL_FILEPATH={{getCSEInstallFilepath}}
CSE_DISTRO_INSTALL_FILEPATH={{getCSEDistroInstallFilepath}}
CSE_CONFIG_FILEPATH={{getCSEConfigFilepath}}
CUSTOM_SEARCH_DOMAIN_FILEPATH={{getCustomSearchDomainFilepath}}
DHCPV6_SERVICE_FILEPATH="{{getDHCPV6ServiceFilepath}}"
DHCPV6_CONFIG_FILEPATH="{{getDHCPV6ConfigFilepath}}"
NEEDS_CONTAINERD="true"
NEEDS_DOCKER_LOGIN="false"
######
# the following variables should be removed once we set the default values in the Go binary on VHD
CONTAINER_RUNTIME=containerd
CLI_TOOL=ctr
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
LOAD_BALANCER_DISABLE_OUTBOUND_SNAT=false

AZURE_ENVIRONMENT_FILEPATH=""
# the above variables should be removed once we set the default values in the Go binary on VHD
######
#####
# the following variables should be removed once we are able to compute each of them from other variables in the Go binary on VHD

# the above variables should be removed once we are able to compute each of them from other variables in the Go binary on VHD.
#####
#####
# the following variables are added to contract but not used in the script yet
#KubeletConfig.taints
#KubeletConfig.startup_taints
#KubeletConfig.HasKubeletDiskType
#KubeletConfig.kubelet_disk_type  //cse_cmd.sh doesn't enable this feature yet even it checks HAS_KUBELET_DISK_TYPE
# the above variables are added to contract but not used in the script yet
/usr/bin/nohup /bin/bash -c "/bin/bash /opt/azure/containers/provision_start.sh"
