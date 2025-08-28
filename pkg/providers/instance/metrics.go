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

package instance

import "github.com/Azure/karpenter-provider-azure/pkg/metrics/metricvalues"

func getBaseVMCreateMetrics(opts *createVMOptions) []metricvalues.MetricValue {
	// DO NOT remove any value fields from this metric
	return []metricvalues.MetricValue{
		metricvalues.VMName(opts.VMName),
		metricvalues.Location(opts.Location),
		metricvalues.Zone(opts.Zone),
		metricvalues.InstanceType(opts.InstanceType.Name),
		metricvalues.CapacityType(opts.CapacityType),
		metricvalues.UseSIG(opts.UseSIG),
		metricvalues.ImageFamily(opts.NodeClass.Spec.ImageFamily),
		metricvalues.FipsMode(opts.NodeClass.Spec.FIPSMode),
		metricvalues.ImageID(opts.LaunchTemplate.ImageID),
		metricvalues.SubnetID(opts.LaunchTemplate.SubnetID),
		metricvalues.OSDiskSizeGB(opts.NodeClass.Spec.OSDiskSizeGB),
		metricvalues.StorageProfileIsEphemeral(opts.LaunchTemplate.StorageProfileIsEphemeral),
		metricvalues.ProvisionMode(opts.ProvisionMode),
	}
}
