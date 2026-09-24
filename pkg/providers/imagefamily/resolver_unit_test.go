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
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
)

func TestRequiresFIPS1403Encryption(t *testing.T) {
	for distro, want := range map[string]bool{
		"aks-ubuntu-fips-containerd-22.04":         true,
		"aks-ubuntu-fips-containerd-22.04-gen2":    true,
		"aks-ubuntu-fips-containerd-22.04-tl-gen2": true,
		"aks-ubuntu-containerd-22.04-gen2":         false,
		"aks-ubuntu-fips-containerd-20.04-gen2":    false,
		"aks-azurelinux-v3-fips-gen2":              false,
		"":                                         false,
	} {
		t.Run(distro, func(t *testing.T) {
			NewWithT(t).Expect(requiresFIPS1403Encryption(distro)).To(Equal(want))
		})
	}
}

// Exercise Resolve itself so dropping the resolved-distro capability assignment
// fails even if the standalone helper and VM payload tests still pass.
func TestResolveFIPS1403Encryption(t *testing.T) {
	for _, tc := range []struct {
		name            string
		family          string
		fipsMode        v1beta1.FIPSMode
		version         string
		imageDefinition string
		want            bool
	}{
		{"explicit Ubuntu2204 FIPS", v1beta1.Ubuntu2204ImageFamily, v1beta1.FIPSModeFIPS, "1.35.0", Ubuntu2204Gen2FIPSImageDefinition, true},
		{"explicit Ubuntu2204 non-FIPS", v1beta1.Ubuntu2204ImageFamily, v1beta1.FIPSModeDisabled, "1.35.0", Ubuntu2204Gen2ImageDefinition, false},
		{"generic Ubuntu FIPS resolves to 2204", v1beta1.UbuntuImageFamily, v1beta1.FIPSModeFIPS, "1.35.0", Ubuntu2204Gen2FIPSImageDefinition, true},
		{"generic Ubuntu FIPS resolves to 2004", v1beta1.UbuntuImageFamily, v1beta1.FIPSModeFIPS, "1.34.0", Ubuntu2004Gen2FIPSImageDefinition, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			imageID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/images/providers/Microsoft.Compute/galleries/AKSUbuntu/images/" + tc.imageDefinition + "/versions/202501.02.0"
			nodeClass := &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily:  lo.ToPtr(tc.family),
					FIPSMode:     lo.ToPtr(tc.fipsMode),
					OSDiskSizeGB: lo.ToPtr[int32](128),
				},
				Status: v1beta1.AKSNodeClassStatus{
					KubernetesVersion: lo.ToPtr(tc.version),
					Images: []v1beta1.NodeImage{{
						ID: imageID,
						Requirements: []corev1.NodeSelectorRequirement{
							{Key: corev1.LabelArchStable, Operator: corev1.NodeSelectorOpIn, Values: []string{karpv1.ArchitectureAmd64}},
							{Key: v1beta1.LabelSKUHyperVGeneration, Operator: corev1.NodeSelectorOpIn, Values: []string{v1beta1.HyperVGenerationV2}},
						},
					}},
				},
			}
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
			ctx := options.ToContext(context.Background(), &options.Options{
				ProvisionMode: consts.ProvisionModeBootstrappingClient,
				UseSIG:        true,
			})
			resolver := NewDefaultResolver(nil, nil, stubInstanceTypeProvider{}, nil)
			parameters, err := resolver.Resolve(ctx, nodeClass, &karpv1.NodeClaim{},
				localDNSTestInstanceType("Standard_D4s_v3", 4, 16384), &template.StaticParameters{
					ClusterName:       "test-cluster",
					KubernetesVersion: tc.version,
					Arch:              karpv1.ArchitectureAmd64,
				})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(parameters.ImageID).To(Equal(imageID))
			g.Expect(parameters.EnableFIPS1403Encryption).To(Equal(tc.want))
		})
	}
}

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
	g.Expect(configuration.SystemReserved).ToNot(HaveKey("pid"))
	g.Expect(configuration.KubeReserved).ToNot(HaveKey("pid"))
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

	g.Expect(configuration.EvictionHard).ToNot(BeNil())
	g.Expect(configuration.EvictionSoft).To(BeNil())
	g.Expect(configuration.EvictionSoftGracePeriod).To(BeNil())
	g.Expect(configuration.EvictionMaxPodGracePeriod).To(BeNil())
	g.Expect(configuration.EnforceNodeAllocatable).To(BeNil())
	g.Expect(configuration.SystemReserved).ToNot(HaveKey("pid"))
	g.Expect(configuration.KubeReserved).To(HaveKeyWithValue("pid", "1000"))
}
