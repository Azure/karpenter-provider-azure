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
TENANT_ID={{.TenantId}}
KUBERNETES_VERSION={{.KubernetesVersion}}
KUBE_BINARY_URL={{.KubeBinaryConfig.KubeBinaryUrl}}
CUSTOM_KUBE_BINARY_URL={{.KubeBinaryConfig.CustomKubeBinaryUrl}}
PRIVATE_KUBE_BINARY_URL={{.KubeBinaryConfig.PrivateKubeBinaryUrl}}
KUBEPROXY_URL={{.KubeproxyUrl}}
API_SERVER_NAME={{.ApiserverConfig.ApiserverName}}
APISERVER_PUBLIC_KEY={{.ApiserverConfig.ApiserverPublicKey}}
SUBSCRIPTION_ID={{.SubscriptionId}}
RESOURCE_GROUP={{.ResourceGroup}}
LOCATION={{.Location}}
VM_TYPE={{.VmType}}
PRIMARY_AVAILABILITY_SET={{.PrimaryAvailabilitySet}}
PRIMARY_SCALE_SET={{.PrimaryScaleSet}}
SERVICE_PRINCIPAL_CLIENT_ID={{derefString .IdentityConfig.ServicePrincipalId}}
SERVICE_PRINCIPAL_FILE_CONTENT="{{derefString .IdentityConfig.ServicePrincipalSecret}}"
USER_ASSIGNED_IDENTITY_ID={{derefString .IdentityConfig.AssignedIdentityId}}
USE_MANAGED_IDENTITY_EXTENSION={{derefString .IdentityConfig.UseManagedIdentityExtension}}
NETWORK_MODE={{getStringFromNetworkModeType .NetworkConfig.NetworkMode}}
NETWORK_PLUGIN={{getStringFromNetworkPluginType .NetworkConfig.NetworkPlugin}}
NETWORK_POLICY="{{getStringFromNetworkPolicyType .NetworkConfig.NetworkPolicy}}"
NETWORK_SECURITY_GROUP={{.NetworkConfig.NetworkSecurityGroup}}
VIRTUAL_NETWORK={{.NetworkConfig.VirtualNetworkConfig.Name}}
VIRTUAL_NETWORK_RESOURCE_GROUP={{.NetworkConfig.VirtualNetworkConfig.ResourceGroup}}
VNET_CNI_PLUGINS_URL={{.NetworkConfig.VnetCniPluginsUrl}}
CNI_PLUGINS_URL={{.NetworkConfig.CniPluginsUrl}}
SUBNET={{.NetworkConfig.Subnet}}
ROUTE_TABLE={{.NetworkConfig.RouteTable}}
USE_INSTANCE_METADATA={{.UseInstanceMetadata}}
LOAD_BALANCER_SKU={{getStringFromLoadBalancerSkuType .LoadBalancerConfig.LoadBalancerSku}}
EXCLUDE_MASTER_FROM_STANDARD_LB={{.LoadBalancerConfig.ExcludeMasterFromStandardLoadBalancer}}
MAXIMUM_LOADBALANCER_RULE_COUNT={{.LoadBalancerConfig.MaxLoadBalancerRuleCount}}
CONTAINERD_DOWNLOAD_URL_BASE={{.ContainerdConfig.ContainerdDownloadUrlBase}}
CONTAINERD_VERSION={{.ContainerdConfig.ContainerdVersion}}
CONTAINERD_PACKAGE_URL={{.ContainerdConfig.ContainerdPackageUrl}}
CONTAINERD_CONFIG_CONTENT="{{getContainerdConfig .}}"
IS_VHD={{.IsVhd}}
GPU_NODE={{getBoolFromFeatureState .GpuConfig.NvidiaState}}
GPU_IMAGE_SHA="{{.GpuConfig.GpuImageSha}}"
GPU_INSTANCE_PROFILE="{{.GpuConfig.GpuInstanceProfile}}"
CONFIG_GPU_DRIVER_IF_NEEDED={{getBoolFromFeatureState .GpuConfig.ConfigGpuDriver}}
ENABLE_GPU_DEVICE_PLUGIN_IF_NEEDED={{getBoolFromFeatureState .GpuConfig.GpuDevicePlugin}}
SGX_NODE={{.IsSgxNode}}
TELEPORT_ENABLED="{{getBoolFromFeatureState .TeleportConfig.Status}}"
TELEPORTD_PLUGIN_DOWNLOAD_URL={{.TeleportConfig.TeleportdPluginDownloadUrl}}
RUNC_VERSION={{.RuncConfig.RuncVersion}}
RUNC_PACKAGE_URL={{.RuncConfig.RuncPackageUrl}}
ENABLE_HOSTS_CONFIG_AGENT="{{getBoolFromFeatureState .HostsConfigAgentStatus}}"
DISABLE_SSH={{if .SshStatus}}"false"{{else}}"true"{{end}}
SHOULD_CONFIGURE_HTTP_PROXY="{{getBoolStringFromFeatureStatePtr .HttpProxyConfig.Status}}"
SHOULD_CONFIGURE_HTTP_PROXY_CA="{{getBoolStringFromFeatureStatePtr .HttpProxyConfig.CaStatus}}"
HTTP_PROXY_TRUSTED_CA="{{.HttpProxyConfig.ProxyTrustedCa}}"
HTTP_PROXY_URLS="{{.HttpProxyConfig.HttpProxy}}"
HTTPS_PROXY_URLS="{{.HttpProxyConfig.HttpsProxy}}"
NO_PROXY_URLS="{{getStringifiedStringArray .HttpProxyConfig.NoProxyEntries ","}}"
SHOULD_CONFIGURE_CUSTOM_CA_TRUST="{{getBoolStringFromFeatureState .CustomCaTrustConfig.Status}}"
CUSTOM_CA_TRUST_COUNT="{{len .CustomCaTrustConfig.CustomCaCerts}}"
{{range $i, $cert := .CustomCaTrustConfig.CustomCaCerts}}
CUSTOM_CA_CERT_{{$i}}="{{$cert}}"
{{end}}
IS_KRUSTLET="{{.IsKrustlet}}"
IPV6_DUAL_STACK_ENABLED={{getBoolStringFromFeatureStatePtr .Ipv6DualStackEnabled}}
ENABLE_UNATTENDED_UPGRADES={{getBoolStringFromFeatureStatePtr .UnattendedUpgradeStatus}}
ENSURE_NO_DUPE_PROMISCUOUS_BRIDGE={{derefBool .EnsureNoDupePromiscuousBridge}}
SWAP_FILE_SIZE_MB="{{.CustomLinuxOsConfig.SwapFileSize}}"
TARGET_CLOUD="{{.CustomCloudConfig.TargetCloud}}"
TARGET_ENVIRONMENT="{{.CustomCloudConfig.TargetEnvironment}}"
CUSTOM_ENV_JSON="{{.CustomCloudConfig.CustomEnvJsonContent}}"
IS_CUSTOM_CLOUD="{{getBoolStringFromFeatureStatePtr .CustomCloudConfig.Status}}"
AZURE_PRIVATE_REGISTRY_SERVER="{{.AzurePrivateRegistryServer}}"
ENABLE_TLS_BOOTSTRAPPING="{{getEnableTLSBoostrap .TlsBootstrappingConfig}}"
ENABLE_SECURE_TLS_BOOTSTRAPPING="{{getEnableSecureTLSBoostrap .TlsBootstrappingConfig}}"
TLS_BOOTSTRAP_TOKEN="{{getTLSBoostrapToken .TlsBootstrappingConfig}}"
KUBELET_FLAGS="{{getStringifiedMap .KubeletConfig.KubeletFlags " "}}"
KUBELET_NODE_LABELS="{{getStringifiedMap .KubeletConfig.KubeletNodeLabels ","}}"
KUBELET_CLIENT_CONTENT="{{.KubeletConfig.KubeletClientKey}}"
KUBELET_CLIENT_CERT_CONTENT="{{.KubeletConfig.KubeletClientCertContent}}"
KUBELET_CONFIG_FILE_ENABLED="{{getBoolStringFromFeatureState .KubeletConfig.KubeletConfigFileStatus}}"
KUBELET_CONFIG_FILE_CONTENT="{{.KubeletConfig.KubeletConfigFileContent}}"
CUSTOM_SEARCH_DOMAIN_NAME="{{.CustomSearchDomain.CustomSearchDomainName}}"
CUSTOM_SEARCH_REALM_USER="{{.CustomSearchDomain.CustomSearchDomainRealmUser}}"
CUSTOM_SEARCH_REALM_PASSWORD="{{.CustomSearchDomain.CustomSearchDomainRealmPassword}}"
MESSAGE_OF_THE_DAY="{{.MessageOfTheDay}}"
THP_ENABLED="{{.CustomLinuxOsConfig.TransparentHugepageSupport}}"
THP_DEFRAG="{{.CustomLinuxOsConfig.TransparentDefrag}}"
# Karpenter set it as static contents now but this should be further refactored to use the contract
SYSCTL_CONTENT="{{getSysctlContent}}"
KUBE_CA_CRT="{{.ClusterCertificateAuthority}}"
KUBENET_TEMPLATE="{{getKubenetTemplate}}"
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
NEEDS_CONTAINERD="true"
NEEDS_DOCKER_LOGIN="false"
SHOULD_CONFIG_TRANSPARENT_HUGE_PAGE="false"
CSE_HELPERS_FILEPATH="/opt/azure/containers/provision_source.sh"
CSE_DISTRO_HELPERS_FILEPATH="/opt/azure/containers/provision_source_distro.sh"
CSE_INSTALL_FILEPATH="/opt/azure/containers/provision_installs.sh"
CSE_DISTRO_INSTALL_FILEPATH="/opt/azure/containers/provision_installs_distro.sh"
CSE_CONFIG_FILEPATH="/opt/azure/containers/provision_configs.sh"
CUSTOM_SEARCH_DOMAIN_FILEPATH="/opt/azure/containers/setup-custom-search-domains.sh"
CUSTOM_SECURE_TLS_BOOTSTRAP_AAD_SERVER_APP_ID="{{getCustomSecureTLSBootstrapAADServerAppID .}}"
DHCPV6_SERVICE_FILEPATH=""
DHCPV6_CONFIG_FILEPATH=""
AZURE_ENVIRONMENT_FILEPATH=""
SHOULD_CONFIG_SWAP_FILE="false" #Following Karpenter's default value. Set as "false" for now.
HAS_KUBELET_DISK_TYPE="false" #Following Karpenter's default value. Set as "false" for now.
# the above variables should be removed once we set the default values in the Go binary on VHD
######
#####
# the following variables should be removed once we are able to compute each of them from other variables in the Go binary on VHD
MIG_NODE="{{getIsMIGNode .GpuConfig.GpuInstanceProfile}}"
GPU_DRIVER_VERSION=""
GPU_NEEDS_FABRIC_MANAGER="false"
OUTBOUND_COMMAND={{.OutboundCommand}}
#SHOULD_CONFIG_SWAP_FILE={{if or (.CustomLinuxOsConfig.SwapFileSize) (gt .CustomLinuxOsConfig.SwapFileSize 0)}}"true"{{else}}"false"{{end}}
HAS_CUSTOM_SEARCH_DOMAIN="false"
NEEDS_CGROUPV2="{{.NeedsCgroupv2}}"
IS_KATA="{{.IsKata}}"
# the above variables should be removed once we are able to compute each of them from other variables in the Go binary on VHD.
#####
#####
# the following variables are added to contract but not used in the script yet
#KubeletConfig.taints
#KubeletConfig.startup_taints
#KubeletConfig.HasKubeletDiskType
#KubeletConfig.kubelet_disk_type  //cse_cmd.sh doesn't enable this feature yet even it checks HAS_KUBELET_DISK_TYPE
#CustomLinuxOsConfig.UlimitConfig, corresponding to CONTAINERD_ULIMITS in cse_cmd.sh
# the above variables are added to contract but not used in the script yet
/usr/bin/nohup /bin/bash -c "/bin/bash /opt/azure/containers/provision_start.sh"
