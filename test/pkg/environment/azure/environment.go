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

package azure

import (
	"testing"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func init() {
	// TODO: should have core1beta1.NormalizedLabels too?
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels, map[string]string{"topology.disk.csi.azure.com/zone": v1.LabelTopologyZone})
}

const (
	WindowsDefaultImage      = "mcr.microsoft.com/oss/kubernetes/pause:3.9"
	CiliumAgentNotReadyTaint = "node.cilium.io/agent-not-ready"
)

type Environment struct {
	*common.Environment
	Region string
}

func NewEnvironment(t *testing.T) *Environment {
	env := common.NewEnvironment(t)

	azureEnv := &Environment{
		Region:      "westus2",
		Environment: env,
	}
	// azureEnv.MarkManagedPools() // Simply marking them once is enough for our hack here
	return azureEnv
}

func (env *Environment) DefaultAKSNodeClass() *v1alpha2.AKSNodeClass {
	nodeClass := test.AKSNodeClass()
	return nodeClass
}

func (env *Environment) AZLinuxNodeClass() *v1alpha2.AKSNodeClass {
	nodeClass := env.DefaultAKSNodeClass()
	nodeClass.Spec.ImageFamily = lo.ToPtr(v1alpha2.AzureLinuxImageFamily)
	return nodeClass
}
