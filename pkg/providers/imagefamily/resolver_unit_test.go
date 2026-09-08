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

package imagefamily

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
)

func prepareTestKubeletConfiguration(enableNodeHardening bool, provisionMode string) *bootstrap.KubeletConfiguration {
	instanceType := &cloudprovider.InstanceType{
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, "32768"),
		),
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved:      corev1.ResourceList{},
			SystemReserved:    corev1.ResourceList{},
			EvictionThreshold: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
	}
	nodeClass := &v1beta1.AKSNodeClass{}
	ctx := options.ToContext(context.Background(), &options.Options{
		EnableNodeHardening: enableNodeHardening,
		ProvisionMode:       provisionMode,
	})
	return prepareKubeletConfiguration(ctx, instanceType, nodeClass)
}

func TestPrepareKubeletConfigurationSoftEvictionEnabled(t *testing.T) {
	g := NewWithT(t)
	configuration := prepareTestKubeletConfiguration(true, consts.ProvisionModeAKSScriptless)

	expectedHardThresholds := map[string]string{
		"memory.available":  "512Mi",
		"nodefs.available":  "10%",
		"nodefs.inodesFree": "5%",
		"pid.available":     "2000",
	}
	g.Expect(configuration.EvictionHard).To(Equal(expectedHardThresholds))

	expectedThresholds := map[string]string{
		"memory.available":  "1Gi",
		"nodefs.available":  "12%",
		"nodefs.inodesFree": "7%",
	}
	g.Expect(configuration.EvictionSoft).To(Equal(expectedThresholds))

	expectedGracePeriods := map[string]metav1.Duration{
		"memory.available":  {Duration: 30 * time.Second},
		"nodefs.available":  {Duration: 2 * time.Minute},
		"nodefs.inodesFree": {Duration: 2 * time.Minute},
	}
	g.Expect(configuration.EvictionSoftGracePeriod).To(Equal(expectedGracePeriods))
	g.Expect(configuration.EvictionMaxPodGracePeriod).ToNot(BeNil())
	g.Expect(*configuration.EvictionMaxPodGracePeriod).To(Equal(int32(60)))

	expectedEnforcement := []string{"pods", "kube-reserved", "system-reserved"}
	g.Expect(configuration.EnforceNodeAllocatable).To(Equal(expectedEnforcement))
	g.Expect(configuration.SystemReserved).To(HaveKeyWithValue("pid", "1000"))
	g.Expect(configuration.KubeReserved).To(HaveKeyWithValue("pid", "1000"))
}

func TestPrepareKubeletConfigurationSoftEvictionDisabled(t *testing.T) {
	g := NewWithT(t)
	configuration := prepareTestKubeletConfiguration(false, consts.ProvisionModeAKSScriptless)

	expectedHardThresholds := map[string]string{
		"memory.available":  "512Mi",
		"nodefs.available":  "10%",
		"nodefs.inodesFree": "5%",
		"pid.available":     "2000",
	}
	g.Expect(configuration.EvictionHard).To(Equal(expectedHardThresholds))
	g.Expect(configuration.EvictionSoft).To(BeNil())
	g.Expect(configuration.EvictionSoftGracePeriod).To(BeNil())
	g.Expect(configuration.EvictionMaxPodGracePeriod).To(BeNil())
	g.Expect(configuration.EnforceNodeAllocatable).To(BeNil())
	g.Expect(configuration.SystemReserved).ToNot(HaveKey("pid"))
	g.Expect(configuration.KubeReserved).To(HaveKeyWithValue("pid", "1000"))
}

func TestPrepareKubeletConfigurationSoftEvictionDisabledForBootstrappingClient(t *testing.T) {
	g := NewWithT(t)
	configuration := prepareTestKubeletConfiguration(true, consts.ProvisionModeBootstrappingClient)

	g.Expect(configuration.EvictionSoft).To(BeNil())
	g.Expect(configuration.EvictionSoftGracePeriod).To(BeNil())
	g.Expect(configuration.EvictionMaxPodGracePeriod).To(BeNil())
	g.Expect(configuration.EnforceNodeAllocatable).To(BeNil())
	g.Expect(configuration.SystemReserved).ToNot(HaveKey("pid"))
}
