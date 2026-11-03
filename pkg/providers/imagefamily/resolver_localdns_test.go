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
	"net/http"
	"strconv"
	"testing"

	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/localdns"
)

// stubInstanceTypeProvider satisfies instancetype.Provider. Resolve only reaches
// the provider to pick a storage profile, which is orthogonal to LocalDNS; an
// empty SKU resolves to managed disks and keeps that path out of the way.
type stubInstanceTypeProvider struct{}

func (stubInstanceTypeProvider) LivenessProbe(*http.Request) error { return nil }

func (stubInstanceTypeProvider) List(context.Context, *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	return nil, nil
}

func (stubInstanceTypeProvider) Get(context.Context, string) (*skewer.SKU, error) {
	return &skewer.SKU{}, nil
}

func (stubInstanceTypeProvider) UpdateInstanceTypes(context.Context) error { return nil }

// localDNSTestInstanceType builds an instance type carrying the two well-known
// requirements the real provider stamps on every SKU, which is what the LocalDNS
// floor reads. Memory is in MiB, matching the producer in
// pkg/providers/instancetype/instancetype.go.
func localDNSTestInstanceType(name string, vcpu, memoryMiB int64) *cloudprovider.InstanceType {
	return &cloudprovider.InstanceType{
		Name: name,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, strconv.FormatInt(vcpu, 10)),
			scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, strconv.FormatInt(memoryMiB, 10)),
			scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
			scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, corev1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
		),
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(vcpu, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memoryMiB*1024*1024, resource.BinarySI),
		},
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved:      corev1.ResourceList{},
			SystemReserved:    corev1.ResourceList{},
			EvictionThreshold: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
	}
}

// localDNSTestNodeClass returns a NodeClass in the only state where the per-node
// decision is live: Mode=Preferred that the status sub-reconciler has already
// resolved to Enabled cluster-wide. Every instance type is then a candidate, and
// whether a given node actually gets LocalDNS is decided here, at launch.
func localDNSTestNodeClass() *v1beta1.AKSNodeClass {
	nodeClass := &v1beta1.AKSNodeClass{
		Spec: v1beta1.AKSNodeClassSpec{
			ImageFamily:  lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			OSDiskSizeGB: lo.ToPtr[int32](128),
			LocalDNS:     &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred},
		},
		Status: v1beta1.AKSNodeClassStatus{
			KubernetesVersion: lo.ToPtr("1.36.0"),
			LocalDNSState:     lo.ToPtr(v1beta1.LocalDNSStateEnabled),
			Images: []v1beta1.NodeImage{
				{
					ID: "/CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/202501.02.0",
					Requirements: []corev1.NodeSelectorRequirement{
						{Key: corev1.LabelArchStable, Operator: corev1.NodeSelectorOpIn, Values: []string{karpv1.ArchitectureAmd64}},
						{Key: v1beta1.LabelSKUHyperVGeneration, Operator: corev1.NodeSelectorOpIn, Values: []string{v1beta1.HyperVGenerationV2}},
					},
				},
			},
		},
	}
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
	return nodeClass
}

// TestResolveLocalDNSPerInstanceType pins the wiring in Resolve: the
// bootstrapping-client path must hand CustomScriptsNodeBootstrapping the
// LocalDNS spec resolved against the instance type being launched, not the
// NodeClass-wide one. Without this, a regression that passed
// nodeClass.Spec.LocalDNS straight through would still satisfy the
// localdns.ResolveForWire unit tests and the AKS Machine API template tests,
// because neither exercises this call site.
func TestResolveLocalDNSPerInstanceType(t *testing.T) {
	for _, tc := range []struct {
		name         string
		instanceType *cloudprovider.InstanceType
		expectedMode v1beta1.LocalDNSMode
	}{
		{
			// Below the floor on vCPU alone, with memory far above it, so a
			// regression that only consulted one of the two still fails here.
			name:         "below the floor resolves to Disabled",
			instanceType: localDNSTestInstanceType("Standard_D2s_v3", localdns.MinVCPU-2, 8192),
			expectedMode: v1beta1.LocalDNSModeDisabled,
		},
		{
			name:         "at the floor resolves to Required",
			instanceType: localDNSTestInstanceType("Standard_D4s_v3", localdns.MinVCPU, localdns.MinMemoryMiB),
			expectedMode: v1beta1.LocalDNSModeRequired,
		},
		{
			name:         "above the floor resolves to Required",
			instanceType: localDNSTestInstanceType("Standard_D8s_v3", localdns.MinVCPU*2, 32768),
			expectedMode: v1beta1.LocalDNSModeRequired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			nodeClass := localDNSTestNodeClass()
			resolver := NewDefaultResolver(nil, nil, stubInstanceTypeProvider{}, nil)
			ctx := options.ToContext(context.Background(), &options.Options{
				ProvisionMode: consts.ProvisionModeBootstrappingClient,
			})

			parameters, err := resolver.Resolve(ctx, nodeClass, &karpv1.NodeClaim{}, tc.instanceType, &template.StaticParameters{
				ClusterName:       "test-cluster",
				KubernetesVersion: "1.36.0",
				Arch:              karpv1.ArchitectureAmd64,
			})
			g.Expect(err).ToNot(HaveOccurred())

			bootstrapper, ok := parameters.CustomScriptsNodeBootstrapping.(customscriptsbootstrap.ProvisionClientBootstrap)
			g.Expect(ok).To(BeTrue(), "expected customscriptsbootstrap.ProvisionClientBootstrap")
			g.Expect(bootstrapper.LocalDNSProfile).ToNot(BeNil())
			g.Expect(bootstrapper.LocalDNSProfile.Mode).To(Equal(tc.expectedMode))

			// Preferred must never reach the wire: downstream would re-interpret
			// it against the single VM size of an agent pool.
			g.Expect(bootstrapper.LocalDNSProfile.Mode).ToNot(Equal(v1beta1.LocalDNSModePreferred))

			// Resolving one node must not mutate the spec the next node reads.
			g.Expect(nodeClass.Spec.LocalDNS.Mode).To(Equal(v1beta1.LocalDNSModePreferred))
		})
	}
}
