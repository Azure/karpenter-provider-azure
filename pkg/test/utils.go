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
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/samber/lo"
	k8srand "k8s.io/apimachinery/pkg/util/rand"
)

const (
	// Note (charliedmcb): this already exists in test/pkg/environment/common
	// https://github.com/Azure/karpenter-provider-azure/blob/84e449787ec72268efb0c7af81ec87a6b3ee95fa/test/pkg/environment/common/setup.go#L47
	// However, I'd prefer to keep our unit test dependants self-contained instead of depending upon the e2e testing package.
	TestingFinalizer = "testing/finalizer"
)

// RandomName returns a pseudo-random resource name with a given prefix.
func RandomName(prefix string) string {
	// You could make this more robust by including additional random characters.
	return prefix + "-" + k8srand.String(10)
}

func ManagedTags(nodepoolName string) map[string]*string {
	return map[string]*string{
		"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
		"karpenter.sh_nodepool":       lo.ToPtr(nodepoolName),
	}
}

func ManagedTagsAKSMachine(nodepoolName string, nodeClaimName string, creationTimestamp time.Time) map[string]*string {
	return map[string]*string{
		"karpenter.azure.com_cluster":                      lo.ToPtr("test-cluster"),
		"karpenter.sh_nodepool":                            lo.ToPtr(nodepoolName),
		"karpenter.azure.com_aksmachine_nodeclaim":         lo.ToPtr(nodeClaimName),
		"karpenter.azure.com_aksmachine_creationtimestamp": lo.ToPtr(instance.AKSMachineTimestampToTag(creationTimestamp)),
	}
}
