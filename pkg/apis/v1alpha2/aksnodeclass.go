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

package v1alpha2

import (
	"fmt"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AKSNodeClassSpec is the top level specification for the AKS Karpenter Provider.
// This will contain configuration necessary to launch instances in AKS.
// +kubebuilder:validation:XValidation:message="FIPS is not yet supported for Ubuntu2204 or Ubuntu2404",rule="has(self.fipsMode) && self.fipsMode == 'FIPS' ? (has(self.imageFamily) && self.imageFamily != 'Ubuntu2204' && self.imageFamily != 'Ubuntu2404') : true"
type AKSNodeClassSpec struct {
	// VNETSubnetID is the subnet used by nics provisioned with this nodeclass.
	// If not specified, we will use the default --vnet-subnet-id specified in karpenter's options config
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/virtualNetworks\/[^\/]+\/subnets\/[^\/]+$`
	// +optional
	VNETSubnetID *string `json:"vnetSubnetID,omitempty"`
	// +kubebuilder:default=128
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=2048
	// osDiskSizeGB is the size of the OS disk in GB.
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`
	// ImageID is the ID of the image that instances use.
	// Not exposed in the API yet
	ImageID *string `json:"-"`
	// ImageFamily is the image family that instances use.
	// +kubebuilder:default=Ubuntu
	// +kubebuilder:validation:Enum:={Ubuntu,Ubuntu2204,Ubuntu2404,AzureLinux}
	ImageFamily *string `json:"imageFamily,omitempty"`
	// FIPSMode controls FIPS compliance for the provisioned nodes
	// +kubebuilder:validation:Enum:={FIPS,Disabled}
	// +optional
	FIPSMode *FIPSMode `json:"fipsMode,omitempty"`
	// Tags to be applied on Azure resources like instances.
	// +kubebuilder:validation:XValidation:message="tags keys must be less than 512 characters",rule="self.all(k, size(k) <= 512)"
	// +kubebuilder:validation:XValidation:message="tags keys must not contain '<', '>', '%', '&', or '?'",rule="self.all(k, !k.matches('[<>%&?]'))"
	// +kubebuilder:validation:XValidation:message="tags keys must not contain '\\'",rule="self.all(k, !k.contains('\\\\'))"
	// +kubebuilder:validation:XValidation:message="tags values must be less than 256 characters",rule="self.all(k, size(self[k]) <= 256)"
	// +optional
	Tags map[string]string `json:"tags,omitempty" hash:"ignore"`
	// Kubelet defines args to be used when configuring kubelet on provisioned nodes.
	// They are a subset of the upstream types, recognizing not all options may be supported.
	// Wherever possible, the types and names should reflect the upstream kubelet types.
	// +kubebuilder:validation:XValidation:message="imageGCHighThresholdPercent must be greater than imageGCLowThresholdPercent",rule="has(self.imageGCHighThresholdPercent) && has(self.imageGCLowThresholdPercent) ?  self.imageGCHighThresholdPercent > self.imageGCLowThresholdPercent  : true"
	// +optional
	Kubelet *KubeletConfiguration `json:"kubelet,omitempty"`
	// MaxPods is an override for the maximum number of pods that can run on a worker node instance.
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
	// Collection of security related karpenter fields
	Security *Security `json:"security,omitempty"`
	// LocalDNSProfile specifies the local DNS configuration for the node
	// +optional
	LocalDNSProfile *LocalDNSProfile `json:"localDNSProfile,omitempty"`
}

// TODO: Add link for the aka.ms/nap/aksnodeclass-enable-host-encryption docs
type Security struct {
	// EncryptionAtHost specifies whether host-level encryption is enabled for provisioned nodes.
	// For more information, see:
	// https://learn.microsoft.com/en-us/azure/aks/enable-host-encryption
	// https://learn.microsoft.com/en-us/azure/virtual-machines/disk-encryption#encryption-at-host---end-to-end-encryption-for-your-vm-data
	// +optional
	EncryptionAtHost *bool `json:"encryptionAtHost,omitempty"`
}

type FIPSMode string

var (
	FIPSModeFIPS     = FIPSMode("FIPS")
	FIPSModeDisabled = FIPSMode("Disabled")
)

type LocalDNSMode int32

const (
	// Unspecified mode for localDNS.
	LocalDNSModeUnspecified LocalDNSMode = 0
	// If the current orchestrator version supports this feature, prefer enabling localDNS.
	LocalDNSModePreferred LocalDNSMode = 1
	// Enable localDNS.
	LocalDNSModeRequired LocalDNSMode = 2
	// Disable localDNS.
	LocalDNSModeDisabled LocalDNSMode = 3
	// Invalid localDNS mode.
	LocalDNSModeInvalid LocalDNSMode = 99
)

type LocalDNSState int32

const (
	// Unspecified state for localDNS.
	LocalDNSStateUnspecified LocalDNSState = 0
	// localDNS is enabled.
	LocalDNSStateEnabled LocalDNSState = 1
	// localDNS is disabled.
	LocalDNSStateDisabled LocalDNSState = 2
	// Invalid LocalDNS state.
	LocalDNSStateInvalid LocalDNSState = 99
)

// LocalDNSProfile specifies the local DNS configuration for nodes
type LocalDNSProfile struct {
	// Mode of enablement for localDNS.
	// Valid values - 0 (Unspecified), 1 (Preferred), 2 (Required), 3 (Disabled), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,3,99}
	// +optional
	Mode *LocalDNSMode `json:"mode,omitempty"`
	// State is the system-generated state of localDNS.
	// Valid values - 0 (Unspecified), 1 (Enabled), 2 (Disabled), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,99}
	// +optional
	State *LocalDNSState `json:"state,omitempty"`
	// CPULimitInMilliCores is the CPU limit in milli cores for localDNS.
	// +optional
	CPULimitInMilliCores *int32 `json:"cpuLimitInMilliCores,omitempty"`
	// MemoryLimitInMB is the memory limit in MB for localDNS.
	// +optional
	MemoryLimitInMB *int32 `json:"memoryLimitInMB,omitempty"`
	// VnetDNSOverrides apply to DNS traffic from pods with dnsPolicy:default or kubelet (referred to as VnetDNS traffic).
	// +optional
	VnetDNSOverrides map[string]*LocalDNSOverrides `json:"vnetDNSOverrides,omitempty"`
	// KubeDNSOverrides apply to DNS traffic from pods with dnsPolicy:ClusterFirst (referred to as KubeDNS traffic).
	// +optional
	KubeDNSOverrides map[string]*LocalDNSOverrides `json:"kubeDNSOverrides,omitempty"`
}

// LocalDNSOverrides specifies DNS override configuration
type LocalDNSOverrides struct {
	// QueryLogging is the log level for DNS queries in localDNS.
	// Valid values - 0 (Unspecified), 1 (Error), 2 (Log), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,99}
	// +optional
	QueryLogging *LocalDNSQueryLogging `json:"queryLogging,omitempty"`
	// Protocol enforces TCP or prefers UDP protocol for connections from localDNS to upstream DNS server.
	// Valid values - 0 (Unspecified), 1 (PreferUDP), 2 (ForceTCP), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,99}
	// +optional
	Protocol *LocalDNSProtocol `json:"protocol,omitempty"`
	// ForwardDestination is the destination server for DNS queries to be forwarded from localDNS.
	// Valid values - 0 (Unspecified), 1 (ClusterCoreDNS), 2 (VnetDNS), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,99}
	// +optional
	ForwardDestination *LocalDNSForwardDestination `json:"forwardDestination,omitempty"`
	// ForwardPolicy is the forward policy for selecting upstream DNS server.
	// Valid values - 0 (Unspecified), 1 (Sequential), 2 (RoundRobin), 3 (Random), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,3,99}
	// +optional
	ForwardPolicy *LocalDNSForwardPolicy `json:"forwardPolicy,omitempty"`
	// MaxConcurrent is the maximum number of concurrent queries.
	// +optional
	MaxConcurrent *int32 `json:"maxConcurrent,omitempty"`
	// CacheDurationInSeconds is the cache max TTL in seconds.
	// +optional
	CacheDurationInSeconds *int32 `json:"cacheDurationInSeconds,omitempty"`
	// ServeStaleDurationInSeconds is the serve stale duration in seconds.
	// +optional
	ServeStaleDurationInSeconds *int32 `json:"serveStaleDurationInSeconds,omitempty"`
	// ServeStale is the policy for serving stale data.
	// Valid values - 0 (Unspecified), 1 (Verify), 2 (Immediate), 3 (Disable), 99 (Invalid).
	// +kubebuilder:validation:Enum:={0,1,2,3,99}
	// +optional
	ServeStale *LocalDNSServeStale `json:"serveStale,omitempty"`
}

// Placeholder types for LocalDNSOverrides enums - these need to be defined with actual values
type LocalDNSQueryLogging int32

const (
	// Unspecified query logging level.
	LocalDNSQueryLoggingUnspecifiedQueryLogging LocalDNSQueryLogging = 0
	// Enables error logging in localDNS.
	LocalDNSQueryLoggingError LocalDNSQueryLogging = 1
	// Enables query logging in localDNS.
	LocalDNSQueryLoggingLog LocalDNSQueryLogging = 2
	// Invalid query logging.
	LocalDNSQueryLoggingInvalidQueryLogging LocalDNSQueryLogging = 99
)

type LocalDNSProtocol int32

const (
	// Unspecified protocol.
	LocalDNSProtocolUnspecifiedProtocol LocalDNSProtocol = 0
	// Prefer UDP protocol for connections from localDNS to upstream DNS server.
	LocalDNSProtocolPreferUDP LocalDNSProtocol = 1
	// Enforce TCP protocol for connections from localDNS to upstream DNS server.
	LocalDNSProtocolForceTCP LocalDNSProtocol = 2
	// Invalid protocol.
	LocalDNSProtocolInvalidProtocol LocalDNSProtocol = 99
)

type LocalDNSForwardDestination int32

const (
	// Unspecified forward destination.
	LocalDNSForwardDestinationUnspecifiedForwardDestination LocalDNSForwardDestination = 0
	// Forward DNS queries from localDNS to cluster CoreDNS.
	LocalDNSForwardDestinationClusterCoreDNS LocalDNSForwardDestination = 1
	// Forward DNS queries from localDNS to DNS server configured in the VNET. A VNET can have multiple DNS servers configured.
	LocalDNSForwardDestinationVnetDNS LocalDNSForwardDestination = 2
	// Invalid forward destination.
	LocalDNSForwardDestinationInvalidForwardDestination LocalDNSForwardDestination = 99
)

type LocalDNSForwardPolicy int32

const (
	// Unspecified forward policy.
	LocalDNSForwardPolicyUnspecifiedForwardPolicy LocalDNSForwardPolicy = 0
	// Implements sequential upstream DNS server selection.
	LocalDNSForwardPolicySequential LocalDNSForwardPolicy = 1
	// Implements round robin upstream DNS server selection.
	LocalDNSForwardPolicyRoundRobin LocalDNSForwardPolicy = 2
	// Implements random upstream DNS server selection.
	LocalDNSForwardPolicyRandom LocalDNSForwardPolicy = 3
	// Invalid forward policy.
	LocalDNSForwardPolicyInvalidForwardPolicy LocalDNSForwardPolicy = 99
)

type LocalDNSServeStale int32

const (
	// Unspecified serve stale policy.
	LocalDNSServeStaleUnspecifiedServeStale LocalDNSServeStale = 0
	// Serve stale data with verification. First verify that an entry is still unavailable from the source before sending the expired entry to the client.
	LocalDNSServeStaleVerify LocalDNSServeStale = 1
	// Serve stale data immediately. Send the expired entry to the client before checking to see if the entry is available from the source.
	LocalDNSServeStaleImmediate LocalDNSServeStale = 2
	// Disable serving stale data.
	LocalDNSServeStaleDisable LocalDNSServeStale = 3
	// Invalid serve stale policy.
	LocalDNSServeStaleInvalidServeStale LocalDNSServeStale = 99
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
	// +kubebuilder:default="none"
	// +optional
	CPUManagerPolicy string `json:"cpuManagerPolicy,omitempty"`
	// CPUCFSQuota enables CPU CFS quota enforcement for containers that specify CPU limits.
	// Note: AKS CustomKubeletConfig uses cpuCfsQuota (camelCase)
	// +kubebuilder:default=true
	// +optional
	CPUCFSQuota *bool `json:"cpuCFSQuota,omitempty"`
	// cpuCfsQuotaPeriod sets the CPU CFS quota period value, `cpu.cfs_period_us`.
	// The value must be between 1 ms and 1 second, inclusive.
	// Default: "100ms"
	// +optional
	// +kubebuilder:default="100ms"
	// TODO: validation
	CPUCFSQuotaPeriod metav1.Duration `json:"cpuCFSQuotaPeriod,omitempty"`
	// ImageGCHighThresholdPercent is the percent of disk usage after which image
	// garbage collection is always run. The percent is calculated by dividing this
	// field value by 100, so this field must be between 0 and 100, inclusive.
	// When specified, the value must be greater than ImageGCLowThresholdPercent.
	// Note: AKS CustomKubeletConfig does not have "Percent" in the field name
	// +kubebuilder:validation:Minimum:=0
	// +kubebuilder:validation:Maximum:=100
	// +optional
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`
	// ImageGCLowThresholdPercent is the percent of disk usage before which image
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
	// +kubebuilder:default="none"
	// +optional
	TopologyManagerPolicy string `json:"topologyManagerPolicy,omitempty"`
	// A comma separated whitelist of unsafe sysctls or sysctl patterns (ending in `*`).
	// Unsafe sysctl groups are `kernel.shm*`, `kernel.msg*`, `kernel.sem`, `fs.mqueue.*`,
	// and `net.*`. For example: "`kernel.msg*,net.ipv4.route.min_pmtu`"
	// Default: []
	// TODO: validation
	// +optional
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`
	// containerLogMaxSize is a quantity defining the maximum size of the container log
	// file before it is rotated. For example: "5Mi" or "256Ki".
	// Default: "10Mi"
	// AKS CustomKubeletConfig has containerLogMaxSizeMB (with units), defaults to 50
	// +kubebuilder:validation:Pattern=`^\d+(E|P|T|G|M|K|Ei|Pi|Ti|Gi|Mi|Ki)$`
	// +kubebuilder:default="50Mi"
	// +optional
	ContainerLogMaxSize string `json:"containerLogMaxSize,omitempty"`
	// containerLogMaxFiles specifies the maximum number of container log files that can be present for a container.
	// Default: 5
	// +kubebuilder:validation:Minimum:=2
	// +kubebuilder:default=5
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
// +kubebuilder:subresource:status
// +kubebuilder:deprecatedversion:warning="use v1beta1.AKSNodeClass instead"
type AKSNodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AKSNodeClassSpec   `json:"spec,omitempty"`
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
