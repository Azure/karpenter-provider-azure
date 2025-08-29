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

import "github.com/Azure/karpenter-provider-azure/pkg/logging"

func getBaseVMCreateMetrics(opts *createVMOptions) []logging.LogValue {
	// DO NOT remove any value fields from this metric
	return []logging.LogValue{
		logging.VMName(opts.VMName),
		logging.Location(opts.Location),
		logging.Zone(opts.Zone),
		logging.InstanceType(opts.InstanceType.Name),
		logging.CapacityType(opts.CapacityType),
		logging.UseSIG(opts.UseSIG),
		logging.ImageFamily(opts.NodeClass.Spec.ImageFamily),
		logging.FIPSMode(opts.NodeClass.Spec.FIPSMode),
		logging.ImageID(opts.LaunchTemplate.ImageID),
		logging.SubnetID(opts.LaunchTemplate.SubnetID),
		logging.OSDiskSizeGB(opts.NodeClass.Spec.OSDiskSizeGB),
		logging.StorageProfileIsEphemeral(opts.LaunchTemplate.StorageProfileIsEphemeral),
		logging.ProvisionMode(opts.ProvisionMode),
	}
}
