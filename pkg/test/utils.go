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

package test

import (
	"github.com/samber/lo"
	k8srand "k8s.io/apimachinery/pkg/util/rand"
)

// RandomName returns a pseudo-random resource name with a given prefix.
func RandomName(prefix string) string {
	// You could make this more robust by including additional random characters.
	return prefix + "-" + k8srand.String(10)
}

func ManagedTags(nodepoolName string) map[string]*string {
	return map[string]*string{
		"karpenter.sh_cluster":  lo.ToPtr("test-cluster"),
		"karpenter.sh_nodepool": lo.ToPtr(nodepoolName),
	}
}
