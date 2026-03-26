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

package stages

import (
	"context"

	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// InstanceOffering is a struct that contains an instance type and its associated offerings. We use this to store the offerings for each instance type in the cache, so that we can filter them later when we need to.
// DO NOT read or modify the offerings on the InstanceType directly, as that data is cached and modifications will persist into the cache.
type InstanceOffering struct {
	// DO NOT read/interact with the offerings here, use the offerings field below instead, which is a shallow copy of the offerings on the instance type, so that we can modify it without affecting the cache.
	InstanceType *corecloudprovider.InstanceType

	// Offerings is a filtered/sorted set of offerings for the instance type
	Offerings corecloudprovider.Offerings
}

type Stage interface {
	Process(ctx context.Context, instanceOfferings []InstanceOffering) []InstanceOffering
}
