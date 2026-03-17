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

package v1beta1

import (
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type FIPSMode string

var (
	FIPSModeFIPS     = FIPSMode("FIPS")
	FIPSModeDisabled = FIPSMode("Disabled")
)

// AKSNodeClassSpec is the top level specification for the AKS Karpenter Provider.
// This will contain configuration necessary to launch instances in AKS.
// +kubebuilder:validation:XValidation:message="FIPS is not yet supported for Ubuntu2204 or Ubuntu2404",rule="has(self.fipsMode) && self.fipsMode == 'FIPS' ? (has(self.imageFamily) && self.imageFamily != 'Ubuntu2204' && self.imageFamily != 'Ubuntu2404') : true"
type AKSNodeClassSpec struct {
	// vnetSubnetID is the subnet used by nics provisioned with this nodeclass.
	// If not specified, we will use the default --vnet-subnet-id specified in karpenter's options config
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/virtualNetworks\/[^\/]+\/subnets\/[^\/]+$`
	// +optional
	VNETSubnetID *string `json:"vnetSubnetID,omitempty"`
	// osDiskSizeGB is the size of the OS disk in GB.
	// +default=128
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=2048
	// +optional
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`
	// ImageID is the ID of the image that instances use.
	// Not exposed in the API yet
	ImageID *string `json:"-"`
	// imageFamily is the image family that instances use.
	// +default="Ubuntu"
	// +kubebuilder:validation:Enum:={Ubuntu,Ubuntu2204,Ubuntu2404,AzureLinux}
	// +optional
	ImageFamily *string `json:"imageFamily,omitempty"`
	// fipsMode controls FIPS compliance for the provisioned nodes
	// +kubebuilder:validation:Enum:={FIPS,Disabled}
	// +optional
	FIPSMode *FIPSMode `json:"fipsMode,omitempty"`
	// tags to be applied on Azure resources like instances.
	// +kubebuilder:validation:XValidation:message="tags keys must be less than 512 characters",rule="self.all(k, size(k) <= 512)"
	// +kubebuilder:validation:XValidation:message="tags keys must not contain '<', '>', '%', '&', or '?'",rule="self.all(k, !k.matches('[<>%&?]'))"
	// +kubebuilder:validation:XValidation:message="tags keys must not contain '\\'",rule="self.all(k, !k.contains('\\\\'))"
	// +kubebuilder:validation:XValidation:message="tags values must be less than 256 characters",rule="self.all(k, size(self[k]) <= 256)"
	// +optional
	Tags map[string]string `json:"tags,omitempty" hash:"ignore"`
	// kubelet defines args to be used when configuring kubelet on provisioned nodes.
	// They are a subset of the upstream types, recognizing not all options may be supported.
	// Wherever possible, the types and names should reflect the upstream kubelet types.
	// +kubebuilder:validation:XValidation:message="imageGCHighThresholdPercent must be greater than imageGCLowThresholdPercent",rule="has(self.imageGCHighThresholdPercent) && has(self.imageGCLowThresholdPercent) ?  self.imageGCHighThresholdPercent > self.imageGCLowThresholdPercent  : true"
	// +optional
	Kubelet *KubeletConfiguration `json:"kubelet,omitempty"`
	// maxPods is an override for the maximum number of pods that can run on a worker node instance.
	// See minimum + maximum pods per node documentation: https://learn.microsoft.com/en-us/azure/aks/concepts-network-ip-address-planning#maximum-pods-per-node
	// Default behavior if this is not specified depends on the network plugin:
	//   - If Network Plugin is Azure with "" (v1 or NodeSubnet), the default is 30.
	//   - If Network Plugin is Azure with "overlay", the default is 250.
	//   - If Network Plugin is None, the default is 250.
	//   - Otherwise, the default is 110 (the usual Kubernetes default).
	//
	// +kubebuilder:validation:Minimum:=10
	// +kubebuilder:validation:Maximum:=250
	// +optional
	MaxPods *int32 `json:"maxPods,omitempty"`

	// security is a collection of security related karpenter fields
	// +optional
	Security *Security `json:"security,omitempty"`
	// localDNS configures the per-node local DNS, with VnetDNS and KubeDNS overrides.
	// LocalDNS helps improve performance and reliability of DNS resolution in an AKS cluster.
	// For more details see aka.ms/aks/localdns.
	// +optional
	LocalDNS *LocalDNS `json:"localDNS,omitempty"`
}

// TODO: Add link for the aka.ms/nap/aksnodeclass-enable-host-encryption docs
type Security struct {
	// encryptionAtHost specifies whether host-level encryption is enabled for provisioned nodes.
	// For more information, see:
	// https://learn.microsoft.com/en-us/azure/aks/enable-host-encryption
	// https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#encryption-at-host---end-to-end-encryption-for-your-vm-data
	// +optional
	EncryptionAtHost *bool `json:"encryptionAtHost,omitempty"`
}

// +kubebuilder:validation:Enum:={Preferred,Required,Disabled}
type LocalDNSMode string

const (
	// If the current orchestrator version supports this feature, prefer enabling localDNS.
	LocalDNSModePreferred LocalDNSMode = "Preferred"
	// Enable localDNS.
	LocalDNSModeRequired LocalDNSMode = "Required"
	// Disable localDNS.
	LocalDNSModeDisabled LocalDNSMode = "Disabled"
)

// LocalDNS configures the per-node local DNS, with VnetDNS and KubeDNS overrides.
// LocalDNS helps improve performance and reliability of DNS resolution in an AKS cluster.
// For more details see aka.ms/aks/localdns.
type LocalDNS struct {
	// mode of enablement for localDNS.
	// +required
	Mode LocalDNSMode `json:"mode,omitempty"`
	// vnetDNSOverrides apply to DNS traffic from pods with dnsPolicy:default or kubelet (referred to as VnetDNS traffic).
	// +required
	// +listType=map
	// +listMapKey=zone
	// +kubebuilder:validation:XValidation:message="must contain required zones '.' and 'cluster.local'",rule="['.', 'cluster.local'].all(z, self.exists(x, x.zone == z))"
	// +kubebuilder:validation:XValidation:message="root zone '.' cannot be forwarded to ClusterCoreDNS from vnetDNSOverrides",rule="!self.exists(x, x.zone == '.' && x.forwardDestination == 'ClusterCoreDNS')"
	// +kubebuilder:validation:XValidation:message="external domains cannot be forwarded to ClusterCoreDNS from vnetDNSOverrides",rule="!self.exists(x, x.zone != '.' && !x.zone.endsWith('cluster.local') && x.forwardDestination == 'ClusterCoreDNS')"
	// +kubebuilder:validation:MaxItems=100
	VnetDNSOverrides []LocalDNSZoneOverride `json:"vnetDNSOverrides,omitempty"`
	// kubeDNSOverrides apply to DNS traffic from pods with dnsPolicy:ClusterFirst (referred to as KubeDNS traffic).
	// +required
	// +listType=map
	// +listMapKey=zone
	// +kubebuilder:validation:XValidation:message="must contain required zones '.' and 'cluster.local'",rule="['.', 'cluster.local'].all(z, self.exists(x, x.zone == z))"
	// +kubebuilder:validation:MaxItems=100
	KubeDNSOverrides []LocalDNSZoneOverride `json:"kubeDNSOverrides,omitempty"`
}

// LocalDNSZoneOverride specifies DNS override configuration for a specific zone
// +kubebuilder:validation:XValidation:message="'cluster.local' cannot be forwarded to VnetDNS",rule="!(self.zone.endsWith('cluster.local') && self.forwardDestination == 'VnetDNS')"
// +kubebuilder:validation:XValidation:message="serveStale Verify cannot be used with protocol ForceTCP",rule="!(self.serveStale == 'Verify' && self.protocol == 'ForceTCP')"
type LocalDNSZoneOverride struct {
	// zone is the DNS zone this override applies to (e.g., ".", "cluster.local").
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=254
	// +kubebuilder:validation:Pattern=`^(\.|[A-Za-z0-9]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?)*\.?)$`
	Zone string `json:"zone,omitempty"`
	// queryLogging is the log level for DNS queries in localDNS.
	// +required
	QueryLogging LocalDNSQueryLogging `json:"queryLogging,omitempty"`
	// protocol enforces TCP or prefers UDP protocol for connections from localDNS to upstream DNS server.
	// +required
	Protocol LocalDNSProtocol `json:"protocol,omitempty"`
	// forwardDestination is the destination server for DNS queries to be forwarded from localDNS.
	// +required
	ForwardDestination LocalDNSForwardDestination `json:"forwardDestination,omitempty"`
	// forwardPolicy is the forward policy for selecting upstream DNS server. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	// +required
	ForwardPolicy LocalDNSForwardPolicy `json:"forwardPolicy,omitempty"`
	// maxConcurrent is the maximum number of concurrent queries. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	// +kubebuilder:validation:Minimum=0
	// +required
	MaxConcurrent *int32 `json:"maxConcurrent,omitempty"`
	// Cache max TTL. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	// +kubebuilder:validation:Pattern=`^([0-9]+(s|m|h))+$`
	// +kubebuilder:validation:Type="string"
	// +kubebuilder:validation:Schemaless
	// +required
	CacheDuration karpv1.NillableDuration `json:"cacheDuration"`
	// Serve stale duration. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	// +kubebuilder:validation:Pattern=`^([0-9]+(s|m|h))+$`
	// +kubebuilder:validation:Type="string"
	// +kubebuilder:validation:Schemaless
	// +required
	ServeStaleDuration karpv1.NillableDuration `json:"serveStaleDuration"`
	// serveStale is the policy for serving stale data. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	// +required
	ServeStale LocalDNSServeStale `json:"serveStale,omitempty"`
}

// LocalDNSOverrides specifies DNS override configuration
// Deprecated: Use LocalDNSZoneOverride instead
type LocalDNSOverrides struct {
	// queryLogging is the log level for DNS queries in localDNS.
	// +required
	QueryLogging LocalDNSQueryLogging `json:"queryLogging,omitempty"`
	// protocol enforces TCP or prefers UDP protocol for connections from localDNS to upstream DNS server.
	// +required
	Protocol LocalDNSProtocol `json:"protocol,omitempty"`
	// forwardDestination is the destination server for DNS queries to be forwarded from localDNS.
	// +required
	ForwardDestination LocalDNSForwardDestination `json:"forwardDestination,omitempty"`
	// forwardPolicy is the forward policy for selecting upstream DNS server. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	// +required
	ForwardPolicy LocalDNSForwardPolicy `json:"forwardPolicy,omitempty"`
	// maxConcurrent is the maximum number of concurrent queries. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	// +kubebuilder:validation:Minimum=0
	// +required
	MaxConcurrent *int32 `json:"maxConcurrent,omitempty"`
	// Cache max TTL. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	// +kubebuilder:validation:Pattern=`^([0-9]+(s|m|h))+$`
	// +kubebuilder:validation:Type="string"
	// +kubebuilder:validation:Schemaless
	// +required
	CacheDuration karpv1.NillableDuration `json:"cacheDuration"`
	// Serve stale duration. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	// +kubebuilder:validation:Pattern=`^([0-9]+(s|m|h))+$`
	// +kubebuilder:validation:Type="string"
	// +kubebuilder:validation:Schemaless
	// +required
	ServeStaleDuration karpv1.NillableDuration `json:"serveStaleDuration"`
	// serveStale is the policy for serving stale data. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	// +required
	ServeStale LocalDNSServeStale `json:"serveStale,omitempty"`
}

// +kubebuilder:validation:Enum:={Error,Log}
type LocalDNSQueryLogging string

const (
	// Enables error logging in localDNS. See [errors plugin](https://coredns.io/plugins/errors) for more information.
	LocalDNSQueryLoggingError LocalDNSQueryLogging = "Error"
	// Enables query logging in localDNS. See [log plugin](https://coredns.io/plugins/log) for more information.
	LocalDNSQueryLoggingLog LocalDNSQueryLogging = "Log"
)

// +kubebuilder:validation:Enum:={PreferUDP,ForceTCP}
type LocalDNSProtocol string

const (
	// Prefer UDP protocol for connections from localDNS to upstream DNS server.
	LocalDNSProtocolPreferUDP LocalDNSProtocol = "PreferUDP"
	// Enforce TCP protocol for connections from localDNS to upstream DNS server.
	LocalDNSProtocolForceTCP LocalDNSProtocol = "ForceTCP"
)

// +kubebuilder:validation:Enum:={ClusterCoreDNS,VnetDNS}
type LocalDNSForwardDestination string

const (
	// Forward DNS queries from localDNS to cluster CoreDNS.
	LocalDNSForwardDestinationClusterCoreDNS LocalDNSForwardDestination = "ClusterCoreDNS"
	// Forward DNS queries from localDNS to DNS server configured in the VNET. A VNET can have multiple DNS servers configured.
	LocalDNSForwardDestinationVnetDNS LocalDNSForwardDestination = "VnetDNS"
)

// +kubebuilder:validation:Enum:={Sequential,RoundRobin,Random}
type LocalDNSForwardPolicy string

const (
	// Implements sequential upstream DNS server selection. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	LocalDNSForwardPolicySequential LocalDNSForwardPolicy = "Sequential"
	// Implements round robin upstream DNS server selection. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	LocalDNSForwardPolicyRoundRobin LocalDNSForwardPolicy = "RoundRobin"
	// Implements random upstream DNS server selection. See [forward plugin](https://coredns.io/plugins/forward) for more information.
	LocalDNSForwardPolicyRandom LocalDNSForwardPolicy = "Random"
)

// +kubebuilder:validation:Enum:={Verify,Immediate,Disable}
type LocalDNSServeStale string

const (
	// Serve stale data with verification. First verify that an entry is still unavailable from the source before sending the expired entry to the client. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	LocalDNSServeStaleVerify LocalDNSServeStale = "Verify"
	// Serve stale data immediately. Send the expired entry to the client before checking to see if the entry is available from the source. See [cache plugin](https://coredns.io/plugins/cache) for more information.
	LocalDNSServeStaleImmediate LocalDNSServeStale = "Immediate"
	// Disable serving stale data.
	LocalDNSServeStaleDisable LocalDNSServeStale = "Disable"
)

// KubeletConfiguration defines args to be used when configuring kubelet on provisioned nodes.
// They are a subset of the upstream types, recognizing not all options may be supported.
// Wherever possible, the types and names should reflect the upstream kubelet types.
// https://pkg.go.dev/k8s.io/kubelet/config/v1beta1#KubeletConfiguration
// https://github.com/kubernetes/kubernetes/blob/9f82d81e55cafdedab619ea25cabf5d42736dacf/cmd/kubelet/app/options/options.go#L53
//
// AKS CustomKubeletConfig w/o CPUReserved,MemoryReserved,SeccompDefault
// https://learn.microsoft.com/en-us/azure/aks/custom-node-configuration?tabs=linux-node-pools
type KubeletConfiguration struct {
	// cpuManagerPolicy is the name of the policy to use.
	// +kubebuilder:validation:Enum:={none,static}
	// +default="none"
	// +optional
	CPUManagerPolicy *string `json:"cpuManagerPolicy,omitempty"`
	// cpuCFSQuota enables CPU CFS quota enforcement for containers that specify CPU limits.
	// Note: AKS CustomKubeletConfig uses cpuCfsQuota (camelCase)
	// +default=true
	// +optional
	CPUCFSQuota *bool `json:"cpuCFSQuota,omitempty"`
	// cpuCFSQuotaPeriod sets the CPU CFS quota period value, `cpu.cfs_period_us`.
	// The value must be between 1 ms and 1 second, inclusive.
	// Default: "100ms"
	// +optional
	// +default="100ms"
	// TODO: validation
	//nolint:kubeapilinter // nodurations: using Duration for compatibility with upstream kubelet types
	CPUCFSQuotaPeriod metav1.Duration `json:"cpuCFSQuotaPeriod,omitempty"`
	// imageGCHighThresholdPercent is the percent of disk usage after which image
	// garbage collection is always run. The percent is calculated by dividing this
	// field value by 100, so this field must be between 0 and 100, inclusive.
	// When specified, the value must be greater than ImageGCLowThresholdPercent.
	// Note: AKS CustomKubeletConfig does not have "Percent" in the field name
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`
	// imageGCLowThresholdPercent is the percent of disk usage before which image
	// garbage collection is never run. Lowest disk usage to garbage collect to.
	// The percent is calculated by dividing this field value by 100,
	// so the field value must be between 0 and 100, inclusive.
	// When specified, the value must be less than imageGCHighThresholdPercent
	// Note: AKS CustomKubeletConfig does not have "Percent" in the field name
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`
	// topologyManagerPolicy is the name of the topology manager policy to use.
	// Valid values include:
	//
	// - `restricted`: kubelet only allows pods with optimal NUMA node alignment for requested resources;
	// - `best-effort`: kubelet will favor pods with NUMA alignment of CPU and device resources;
	// - `none`: kubelet has no knowledge of NUMA alignment of a pod's CPU and device resources.
	// - `single-numa-node`: kubelet only allows pods with a single NUMA alignment
	//   of CPU and device resources.
	//
	// +kubebuilder:validation:Enum:={restricted,best-effort,none,single-numa-node}
	// +default="none"
	// +optional
	TopologyManagerPolicy *string `json:"topologyManagerPolicy,omitempty"`
	// allowedUnsafeSysctls is a comma separated whitelist of unsafe sysctls or sysctl patterns (ending in `*`).
	// Unsafe sysctl groups are `kernel.shm*`, `kernel.msg*`, `kernel.sem`, `fs.mqueue.*`,
	// and `net.*`. For example: "`kernel.msg*,net.ipv4.route.min_pmtu`"
	// Default: []
	// TODO: validation
	// +optional
	//nolint:kubeapilinter // ssatags: adding listType marker would be a breaking change
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`
	// containerLogMaxSize is a quantity defining the maximum size of the container log
	// file before it is rotated. For example: "5Mi" or "256Ki".
	// Default: "10Mi"
	// AKS CustomKubeletConfig has containerLogMaxSizeMB (with units), defaults to 50
	// +kubebuilder:validation:Pattern=`^\d+(E|P|T|G|M|K|Ei|Pi|Ti|Gi|Mi|Ki)$`
	// +default="50Mi"
	// +optional
	ContainerLogMaxSize *string `json:"containerLogMaxSize,omitempty"`
	// containerLogMaxFiles specifies the maximum number of container log files that can be present for a container.
	// Default: 5
	// +kubebuilder:validation:Minimum:=2
	// +default=5
	// +optional
	ContainerLogMaxFiles *int32 `json:"containerLogMaxFiles,omitempty"`
	// podPidsLimit is the maximum number of PIDs in any pod.
	// AKS CustomKubeletConfig uses PodMaxPids, int32 (!)
	// Default: -1
	// +optional
	PodPidsLimit *int64 `json:"podPidsLimit,omitempty"`
}

// AKSNodeClass is the Schema for the AKSNodeClass API
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=aksnodeclasses,scope=Cluster,categories={karpenter,nap},shortName={aksnc,aksncs}
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="ImageFamily",type=string,JSONPath=".spec.imageFamily",priority=1
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
type AKSNodeClass struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the top level specification for the AKS Karpenter Provider.
	// This will contain configuration necessary to launch instances in AKS.
	// +optional
	//nolint:kubeapilinter // optionalfields: changing to pointer would be a breaking change
	Spec AKSNodeClassSpec `json:"spec,omitempty"`
	// status contains the resolved state of the AKSNodeClass.
	// +optional
	//nolint:kubeapilinter // optionalfields: changing to pointer would be a breaking change
	Status AKSNodeClassStatus `json:"status,omitempty"`
}

// We need to bump the AKSNodeClassHashVersion when we make an update to the AKSNodeClass CRD under these conditions:
// 1. A field changes its default value for an existing field that is already hashed
// 2. A field is added to the hash calculation with an already-set value
// 3. A field is removed from the hash calculations
const AKSNodeClassHashVersion = "v3"

func (in *AKSNodeClass) Hash() string {
	return fmt.Sprint(lo.Must(hashstructure.Hash(in.Spec, hashstructure.FormatV2, &hashstructure.HashOptions{
		SlicesAsSets:    true,
		IgnoreZeroValue: true,
		ZeroNil:         true,
	})))
}

// AKSNodeClassList contains a list of AKSNodeClass
// +kubebuilder:object:root=true
type AKSNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AKSNodeClass `json:"items"`
}

// GetEncryptionAtHost returns whether encryption at host is enabled for the node class.
// Returns false if Security or EncryptionAtHost is nil.
func (in *AKSNodeClass) GetEncryptionAtHost() bool {
	if in.Spec.Security != nil && in.Spec.Security.EncryptionAtHost != nil {
		return *in.Spec.Security.EncryptionAtHost
	}
	return false
}

// IsLocalDNSEnabled returns whether LocalDNS should be enabled for this node class.
// Returns true for Required mode, false for Disabled mode, and for Preferred mode,
// returns true only if the Kubernetes version is >= 1.35.
func (in *AKSNodeClass) IsLocalDNSEnabled() bool {
	if in.Spec.LocalDNS == nil || in.Spec.LocalDNS.Mode == "" {
		return false
	}

	switch in.Spec.LocalDNS.Mode {
	case LocalDNSModeRequired:
		return true
	case LocalDNSModeDisabled:
		return false
	case LocalDNSModePreferred:
		// For Preferred mode, check if K8s version >= 1.35
		kubernetesVersion, err := in.GetKubernetesVersion()
		if err != nil {
			return false // If we can't get version, don't enable
		}

		// Parse version
		parsedVersion, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
		if err != nil {
			return false
		}

		return parsedVersion.GE(semver.Version{Major: 1, Minor: 35})
	default:
		return false
	}
}
