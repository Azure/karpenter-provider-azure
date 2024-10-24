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
type AKSNodeClassSpec struct {
	// VNETSubnetID is the subnet used by nics provisioned with this nodeclass.
	// If not specified, we will use the default --vnet-subnet-id specified in karpenter's options config
	// +kubebuilder:validation:Pattern=`(?i)^\/subscriptions\/[^\/]+\/resourceGroups\/[a-zA-Z0-9_\-().]{0,89}[a-zA-Z0-9_\-()]\/providers\/Microsoft\.Network\/virtualNetworks\/[^\/]+\/subnets\/[^\/]+$`
	// +optional
	VNETSubnetID *string `json:"vnetSubnetID,omitempty"`
	// +kubebuilder:default=128
	// +kubebuilder:validation:Minimum=100
	// osDiskSizeGB is the size of the OS disk in GB.
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`
	// ImageID is the ID of the image that instances use.
	// Not exposed in the API yet
	ImageID *string `json:"-"`
	// ImageFamily is the image family that instances use.
	// +kubebuilder:default=Ubuntu2204
	// +kubebuilder:validation:Enum:={Ubuntu2204,AzureLinux}
	ImageFamily *string `json:"imageFamily,omitempty"`
	// Tags to be applied on Azure resources like instances.
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
	// Kubelet defines args to be used when configuring kubelet on provisioned nodes.
	// They are a subset of the upstream types, recognizing not all options may be supported.
	// Wherever possible, the types and names should reflect the upstream kubelet types.
	// +kubebuilder:validation:XValidation:message="imageGCHighThresholdPercent must be greater than imageGCLowThresholdPercent",rule="has(self.imageGCHighThresholdPercent) && has(self.imageGCLowThresholdPercent) ?  self.imageGCHighThresholdPercent > self.imageGCLowThresholdPercent  : true"
	// +optional
	Kubelet *KubeletConfiguration `json:"kubelet,omitempty" hash:"ignore"`
	// MaxPods is an override for the maximum number of pods that can run on a worker node instance.
	// +kubebuilder:validation:Minimum:=0
	// +optional
	MaxPods *int32 `json:"maxPods,omitempty"`
}

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
// +kubebuilder:resource:path=aksnodeclasses,scope=Cluster,categories=karpenter,shortName={aksnc,aksncs}
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
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
const AKSNodeClassHashVersion = "v1"

// TODO: add hash tests
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
