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

package cloudprovider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

type stubInstanceTypeProvider struct {
	list func(context.Context, *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error)
}

func (s *stubInstanceTypeProvider) LivenessProbe(_ *http.Request) error { return nil }
func (s *stubInstanceTypeProvider) List(ctx context.Context, nc *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	if s.list == nil {
		return nil, nil
	}
	return s.list(ctx, nc)
}
func (s *stubInstanceTypeProvider) Get(context.Context, string) (*skewer.SKU, error) { return nil, nil }
func (s *stubInstanceTypeProvider) UpdateInstanceTypes(context.Context) error        { return nil }

type stubOverlayStore struct {
	applyAllCalls int
	applyAll      func(nodePoolName string, its []*cloudprovider.InstanceType) ([]*cloudprovider.InstanceType, error)
}

func (s *stubOverlayStore) ApplyAll(nodePoolName string, its []*cloudprovider.InstanceType) ([]*cloudprovider.InstanceType, error) {
	s.applyAllCalls++
	if s.applyAll == nil {
		return its, nil
	}
	return s.applyAll(nodePoolName, its)
}

func (s *stubOverlayStore) Apply(_ string, it *cloudprovider.InstanceType) (*cloudprovider.InstanceType, error) {
	return it, nil
}

func TestGenerateNodeClaimName(t *testing.T) {
	tests := []struct {
		name     string
		vmName   string
		expected string
	}{
		{
			name:     "basic",
			vmName:   "aks-default-a1b2c",
			expected: "default-a1b2c",
		},
		{
			name:     "dashes nodepool name",
			vmName:   "aks-node-pool-name-a1b2c",
			expected: "node-pool-name-a1b2c",
		},
		{
			name:     "aks",
			vmName:   "aks-aks-default-a1b2c",
			expected: "aks-default-a1b2c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := GetNodeClaimNameFromVMName(tt.vmName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestVmInstanceToNodeClaim_NilProperties(t *testing.T) {
	tests := []struct {
		name                string
		vm                  *armcompute.VirtualMachine
		expectFallbackToNow bool
		expectExactTime     *time.Time
	}{
		{
			name: "nil Properties - fallback to time.Now()",
			vm: &armcompute.VirtualMachine{
				Name: lo.ToPtr("aks-test-vm"),
				ID:   lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/aks-test-vm"),
			},
			expectFallbackToNow: true,
		},
		{
			name: "nil TimeCreated - fallback to time.Now()",
			vm: &armcompute.VirtualMachine{
				Name:       lo.ToPtr("aks-test-vm"),
				ID:         lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/aks-test-vm"),
				Properties: &armcompute.VirtualMachineProperties{},
			},
			expectFallbackToNow: true,
		},
		{
			name: "valid TimeCreated - use exact time",
			vm: &armcompute.VirtualMachine{
				Name: lo.ToPtr("aks-test-vm"),
				ID:   lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/aks-test-vm"),
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
				},
			},
			expectExactTime: lo.ToPtr(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			cp := &CloudProvider{}
			before := time.Now()
			nodeClaim, err := cp.vmInstanceToNodeClaim(ctx, tt.vm, nil)
			after := time.Now()

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(nodeClaim).ToNot(BeNil())
			g.Expect(nodeClaim.CreationTimestamp).ToNot(Equal(metav1.Time{}))

			if tt.expectFallbackToNow {
				// When TimeCreated is unavailable, should fallback to time.Now() for GC safety
				g.Expect(nodeClaim.CreationTimestamp.Time).To(BeTemporally(">=", before))
				g.Expect(nodeClaim.CreationTimestamp.Time).To(BeTemporally("<=", after))
			}

			if tt.expectExactTime != nil {
				// When TimeCreated is available, should use the exact time from VM
				g.Expect(nodeClaim.CreationTimestamp.Time).To(Equal(*tt.expectExactTime))
			}
		})
	}
}

func TestResolveInstanceTypes_OverlayAppliedBeforeResourceFits(t *testing.T) {
	g := NewWithT(t)

	customResource := corev1.ResourceName("nextflow.io/fuse")
	base := &cloudprovider.InstanceType{
		Name:         "Standard_D2_v3",
		Requirements: scheduling.NewRequirements(),
		Offerings: cloudprovider.Offerings{&cloudprovider.Offering{
			Requirements: scheduling.NewRequirements(),
			Price:        1,
			Available:    true,
		}},
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("8Gi"),
		},
		Overhead: &cloudprovider.InstanceTypeOverhead{},
	}
	overlaid := base.DeepCopy()
	overlaid.Capacity[customResource] = resource.MustParse("100")

	cp := &CloudProvider{
		instanceTypeProvider: &stubInstanceTypeProvider{list: func(context.Context, *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
			return []*cloudprovider.InstanceType{base}, nil
		}},
		instanceTypeStore: &stubOverlayStore{applyAll: func(nodePoolName string, its []*cloudprovider.InstanceType) ([]*cloudprovider.InstanceType, error) {
			if nodePoolName != "default" {
				return nil, fmt.Errorf("unexpected nodepool %q", nodePoolName)
			}
			return []*cloudprovider.InstanceType{overlaid}, nil
		}},
	}

	ctx := coreoptions.ToContext(context.Background(), &coreoptions.Options{
		FeatureGates: coreoptions.FeatureGates{NodeOverlay: true},
	})
	nodeClaim := &karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{karpv1.NodePoolLabelKey: "default"}},
		Spec:       karpv1.NodeClaimSpec{Resources: karpv1.ResourceRequirements{Requests: corev1.ResourceList{customResource: resource.MustParse("1")}}},
	}

	instanceTypes, err := cp.resolveInstanceTypes(ctx, nodeClaim, &v1beta1.AKSNodeClass{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(instanceTypes).To(HaveLen(1))
	g.Expect(instanceTypes[0].Name).To(Equal("Standard_D2_v3"))
}

func TestResolveInstanceTypes_DoesNotApplyOverlayWhenFeatureGateDisabled(t *testing.T) {
	g := NewWithT(t)

	store := &stubOverlayStore{}
	cp := &CloudProvider{
		instanceTypeProvider: &stubInstanceTypeProvider{list: func(context.Context, *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
			return []*cloudprovider.InstanceType{{
				Name:         "Standard_D2_v3",
				Requirements: scheduling.NewRequirements(),
				Offerings:    cloudprovider.Offerings{&cloudprovider.Offering{Requirements: scheduling.NewRequirements(), Price: 1, Available: true}},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				Overhead: &cloudprovider.InstanceTypeOverhead{},
			}}, nil
		}},
		instanceTypeStore: store,
	}

	ctx := coreoptions.ToContext(context.Background(), &coreoptions.Options{})
	nodeClaim := &karpv1.NodeClaim{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{karpv1.NodePoolLabelKey: "default"}}}

	_, err := cp.resolveInstanceTypes(ctx, nodeClaim, &v1beta1.AKSNodeClass{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(store.applyAllCalls).To(Equal(0))
}
