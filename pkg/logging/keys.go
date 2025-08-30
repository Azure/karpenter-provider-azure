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

package logging

const (
	// MARK: Common Keys
	// > Note: we should only define common log keys here, that we want to ensure consistency between logging occurrences
	// > No need to define every adhoc key.

	ImageID = "imageID"

	// MARK: Upstream Log Keys
	// > In the cases where a key is defined in upstream logs we match their casing regardless of if it aligns with our general casing pattern here.
	// > Each upstream key should have a code reference to upstream documented with it.

	// InstanceType is defined with kebab-casing within upstream here:
	// https://github.com/kubernetes-sigs/karpenter/blob/0f4e19a3e8d6eb07ffc5157caa1d296badce8221/pkg/controllers/nodeclaim/lifecycle/launch.go#L119
	InstanceType = "instance-type"
)
