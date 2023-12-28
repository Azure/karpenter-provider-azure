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

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	coretest "github.com/aws/karpenter-core/pkg/test"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/test"
	"github.com/Azure/karpenter/test/pkg/environment/common"
)

const WindowsDefaultImage = "mcr.microsoft.com/oss/kubernetes/pause:3.9"

type Environment struct {
	*common.Environment
	Region string

	// TODO (charliedmcb): explore porting over/implementing an Azure equivalent version
	// SQSProvider *interruption.SQSProvider
}

func NewEnvironment(t *testing.T) *Environment {
	env := common.NewEnvironment(t)

	return &Environment{
		Region:      "westus2",
		Environment: env,

		// SQSProvider:   interruption.NewSQSProvider(sqs.New(session)),
	}
}

func (env *Environment) DefaultNodePool(nodeClass *v1alpha2.AKSNodeClass) *corev1beta1.NodePool {
	nodePool := coretest.NodePool()
	nodePool.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
		Name: nodeClass.Name,
	}
	nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirement{
		{
			Key:      v1.LabelOSStable,
			Operator: v1.NodeSelectorOpIn,
			Values:   []string{string(v1.Linux)},
		},
		{
			Key:      corev1beta1.CapacityTypeLabelKey,
			Operator: v1.NodeSelectorOpIn,
			Values:   []string{corev1beta1.CapacityTypeOnDemand},
		},
		// TODO: remove constraint when arm64 is supported
		{
			Key:      v1.LabelArchStable,
			Operator: v1.NodeSelectorOpIn,
			Values:   []string{corev1beta1.ArchitectureAmd64},
		},
		{
			Key:      v1alpha2.LabelSKUFamily,
			Operator: v1.NodeSelectorOpIn,
			Values:   []string{"D"},
		},
	}
	nodePool.Spec.Disruption.ConsolidateAfter = &corev1beta1.NillableDuration{}
	nodePool.Spec.Disruption.ExpireAfter.Duration = nil
	nodePool.Spec.Limits = corev1beta1.Limits(v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("100"),
		v1.ResourceMemory: resource.MustParse("1000Gi"),
	})

	nodePool.Spec.Template.Spec.StartupTaints = []v1.Taint{
		{
			Key:    "node.cilium.io/agent-not-ready",
			Value:  "true",
			Effect: v1.TaintEffectNoExecute,
		},
	}
	return nodePool
}

func (env *Environment) DefaultAKSNodeClass() *v1alpha2.AKSNodeClass {
	nodeClass := test.AKSNodeClass()

	// TODO: override values for testing

	return nodeClass
}
