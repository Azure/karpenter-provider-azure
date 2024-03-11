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

package expectations

import (
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/onsi/gomega"
)

func ExpectUnavailable(env *test.Environment, instanceType string, zone string, capacityType string) {
	Expect(env.UnavailableOfferingsCache.IsUnavailable(instanceType, fmt.Sprintf("%s-%s", fake.Region, zone), capacityType)).To(BeTrue())
}
