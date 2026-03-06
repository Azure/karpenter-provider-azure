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

package instance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

// createTestAzureResponseError creates a proper Azure SDK error with the given error code and message
func createTestAzureResponseError(errorCode, errorMessage string, statusCode int) error {
	errorBody := fmt.Sprintf(`{"error": {"code": "%s", "message": "%s"}}`, errorCode, errorMessage)
	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			Body: io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}

//nolint:gocyclo
func TestBuildNodeClaimFromAKSMachine(t *testing.T) {
	ctx := context.Background()
	creationTime := NewAKSMachineTimestamp()
	aksMachineLocation := "eastus"

	possibleInstanceTypes := []*corecloudprovider.InstanceType{
		{
			Name: "Standard_D2_v2",
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("2"),
				v1.ResourceMemory: resource.MustParse("7Gi"),
			},
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelInstanceTypeStable, v1.NodeSelectorOpIn, "Standard_D2_v2"),
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, "amd64"),
				scheduling.NewRequirement(v1.LabelOSStable, v1.NodeSelectorOpIn, "linux"),
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, v1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
			),
			Offerings: corecloudprovider.Offerings{
				{
					Requirements: scheduling.NewRequirements(
						scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, v1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
						scheduling.NewRequirement(v1.LabelTopologyZone, v1.NodeSelectorOpIn, "eastus-1"),
					),
					Price:     0.096,
					Available: true,
				},
			},
			Overhead: &corecloudprovider.InstanceTypeOverhead{
				KubeReserved:      v1.ResourceList{},
				SystemReserved:    v1.ResourceList{},
				EvictionThreshold: v1.ResourceList{},
			},
		},
	}

	makeBaseMachine := func() *armcontainerservice.Machine {
		return &armcontainerservice.Machine{
			ID:    lo.ToPtr("/subscriptions/test/resourceGroups/test/providers/Microsoft.ContainerService/managedClusters/test/agentPools/pool/machines/test-machine"),
			Name:  lo.ToPtr("test-machine"),
			Zones: []*string{lo.ToPtr("1")},
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: lo.ToPtr("Standard_D2_v2"),
				},
				Priority:   lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
				ResourceID: lo.ToPtr("/subscriptions/test/resourceGroups/test/providers/Microsoft.Compute/virtualMachines/test-vm"),
				Status: &armcontainerservice.MachineStatus{
					CreationTimestamp: lo.ToPtr(creationTime.Add(10 * time.Minute)),
				},
				NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-202501.28.0"),
				Tags: map[string]*string{
					NodePoolTagKey: lo.ToPtr("test-nodepool"),
					launchtemplate.KarpenterAKSMachineNodeClaimTagKey:         lo.ToPtr("test-nodeclaim"),
					launchtemplate.KarpenterAKSMachineCreationTimestampTagKey: lo.ToPtr(AKSMachineTimestampToTag(creationTime)),
				},
			},
		}
	}

	t.Run("should build NodeClaim successfully", func(t *testing.T) {
		aksMachine := makeBaseMachine()
		nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if nodeClaim == nil {
			t.Fatal("nodeClaim is nil")
			return
		}
		if nodeClaim.Name != "test-nodeclaim" {
			t.Errorf("Name = %q, want %q", nodeClaim.Name, "test-nodeclaim")
		}
		if nodeClaim.Labels[karpv1.CapacityTypeLabelKey] != karpv1.CapacityTypeOnDemand {
			t.Errorf("CapacityType = %q, want %q", nodeClaim.Labels[karpv1.CapacityTypeLabelKey], karpv1.CapacityTypeOnDemand)
		}
		if nodeClaim.Labels[karpv1.NodePoolLabelKey] != "test-nodepool" {
			t.Errorf("NodePool = %q, want %q", nodeClaim.Labels[karpv1.NodePoolLabelKey], "test-nodepool")
		}
		if nodeClaim.Labels[v1.LabelTopologyZone] != "eastus-1" {
			t.Errorf("Zone = %q, want %q", nodeClaim.Labels[v1.LabelTopologyZone], "eastus-1")
		}
		if _, ok := nodeClaim.Status.Capacity[v1.ResourceCPU]; !ok {
			t.Error("CPU not found in capacity")
		}
		if _, ok := nodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]; !ok {
			t.Error("AKSMachineResourceID annotation not found")
		}
		if !nodeClaim.CreationTimestamp.Equal(&metav1.Time{Time: creationTime}) {
			t.Errorf("CreationTimestamp = %v, want %v", nodeClaim.CreationTimestamp, metav1.NewTime(creationTime))
		}
	})

	t.Run("should handle missing zone gracefully", func(t *testing.T) {
		aksMachine := makeBaseMachine()
		aksMachine.Zones = []*string{}
		nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if nodeClaim == nil {
			t.Fatal("nodeClaim is nil")
			return
		}
		if nodeClaim.Name != "test-nodeclaim" {
			t.Errorf("Name = %q, want %q", nodeClaim.Name, "test-nodeclaim")
		}
		if _, ok := nodeClaim.Labels[v1.LabelTopologyZone]; ok {
			t.Error("expected no topology zone label for empty zones")
		}
		if !nodeClaim.CreationTimestamp.Equal(&metav1.Time{Time: creationTime}) {
			t.Errorf("CreationTimestamp = %v, want %v", nodeClaim.CreationTimestamp, metav1.NewTime(creationTime))
		}
	})

	t.Run("should handle missing creation time gracefully", func(t *testing.T) {
		aksMachine := makeBaseMachine()
		delete(aksMachine.Properties.Tags, launchtemplate.KarpenterAKSMachineCreationTimestampTagKey)
		nodeClaim, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if nodeClaim == nil {
			t.Fatal("nodeClaim is nil")
			return
		}
		if nodeClaim.Name != "test-nodeclaim" {
			t.Errorf("Name = %q, want %q", nodeClaim.Name, "test-nodeclaim")
		}
		expected := metav1.Time{}
		if !nodeClaim.CreationTimestamp.Equal(&expected) {
			t.Errorf("CreationTimestamp = %v, want zero value", nodeClaim.CreationTimestamp)
		}
	})

	t.Run("should return error when properties is missing", func(t *testing.T) {
		aksMachine := makeBaseMachine()
		aksMachine.Properties = nil
		_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("should return error when VM size is missing", func(t *testing.T) {
		aksMachine := makeBaseMachine()
		aksMachine.Properties.Hardware = nil
		_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("should return error when priority is missing", func(t *testing.T) {
		aksMachine := makeBaseMachine()
		aksMachine.Properties.Priority = nil
		_, err := BuildNodeClaimFromAKSMachine(ctx, aksMachine, possibleInstanceTypes, aksMachineLocation)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFindNodePoolFromAKSMachine(t *testing.T) {
	ctx := context.Background()

	t.Run("should find NodePool when tag exists and NodePool exists", func(t *testing.T) {
		nodePool := &karpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodepool",
			},
		}
		client := fake.NewClientBuilder().WithObjects(nodePool).Build()

		aksMachine := &armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{
					"karpenter.sh_nodepool": lo.ToPtr("test-nodepool"),
				},
			},
		}

		foundNodePool, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if foundNodePool == nil {
			t.Fatal("foundNodePool is nil")
			return
		}
		if foundNodePool.Name != "test-nodepool" {
			t.Errorf("Name = %q, want %q", foundNodePool.Name, "test-nodepool")
		}
	})

	t.Run("should return NotFound when NodePool tag is missing", func(t *testing.T) {
		aksMachine := &armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{},
			},
		}
		client := fake.NewClientBuilder().Build()
		_, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected NotFound error, got %v", err)
		}
	})

	t.Run("should return NotFound when NodePool tag is empty", func(t *testing.T) {
		aksMachine := &armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{
					"karpenter.sh_nodepool": lo.ToPtr(""),
				},
			},
		}
		client := fake.NewClientBuilder().Build()
		_, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected NotFound error, got %v", err)
		}
	})

	t.Run("should return NotFound when NodePool does not exist", func(t *testing.T) {
		aksMachine := &armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{
					"karpenter.sh_nodepool": lo.ToPtr("test-nodepool"),
				},
			},
		}
		client := fake.NewClientBuilder().Build()
		_, err := FindNodePoolFromAKSMachine(ctx, aksMachine, client)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected NotFound error, got %v", err)
		}
	})
}

//nolint:gocyclo
func TestGetAKSMachineNameFromNodeClaimName(t *testing.T) {
	tests := []struct {
		name          string
		nodeClaimName string
		wantLen       int
		wantExact     string
		wantPrefix    string
		wantSuffix    string
	}{
		{
			name:          "under length limit",
			nodeClaimName: "default-a1b2c",
			wantExact:     "default-a1b2c",
		},
		{
			name:          "short name",
			nodeClaimName: "d-a1b2c",
			wantExact:     "d-a1b2c",
		},
		{
			name:          "at length limit",
			nodeClaimName: "123456789-123456789-123456789-a1b2c",
			wantExact:     "123456789-123456789-123456789-a1b2c",
		},
		{
			name:          "at length limit +1 - truncate and hash",
			nodeClaimName: "123456789-123456789-1234567890-a1b2c",
			wantLen:       35,
			wantPrefix:    "123456789-123456789-123",
			wantSuffix:    "-a1b2c",
		},
		{
			name:          "above length limit",
			nodeClaimName: "123456789-123456789-123456789-123456789-a1b2c",
			wantLen:       35,
			wantPrefix:    "123456789-123456789-123",
			wantSuffix:    "-a1b2c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machineName, err := GetAKSMachineNameFromNodeClaimName(tt.nodeClaimName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantExact != "" {
				if machineName != tt.wantExact {
					t.Errorf("machineName = %q, want %q", machineName, tt.wantExact)
				}
				return
			}
			if tt.wantLen > 0 && len(machineName) != tt.wantLen {
				t.Errorf("len(machineName) = %d, want %d", len(machineName), tt.wantLen)
			}
			if tt.wantPrefix != "" && !strings.HasPrefix(machineName, tt.wantPrefix) {
				t.Errorf("machineName %q does not have prefix %q", machineName, tt.wantPrefix)
			}
			if tt.wantSuffix != "" && !strings.HasSuffix(machineName, tt.wantSuffix) {
				t.Errorf("machineName %q does not have suffix %q", machineName, tt.wantSuffix)
			}
		})
	}

	t.Run("different inputs produce different outputs", func(t *testing.T) {
		name1 := "123456789-123456789-1234567890-a1b2c"
		name2 := "123456789-123456789-1234567891-a1b2c"
		m1, err1 := GetAKSMachineNameFromNodeClaimName(name1)
		m2, err2 := GetAKSMachineNameFromNodeClaimName(name2)
		if err1 != nil {
			t.Fatalf("unexpected error: %v", err1)
		}
		if err2 != nil {
			t.Fatalf("unexpected error: %v", err2)
		}
		if m1 == m2 {
			t.Errorf("expected different machine names, got same: %q", m1)
		}
	})

	t.Run("deterministic results", func(t *testing.T) {
		name := "consistent-very-long-nodepool-name-test-xyz12"
		m1, _ := GetAKSMachineNameFromNodeClaimName(name)
		m2, _ := GetAKSMachineNameFromNodeClaimName(name)
		if m1 != m2 {
			t.Errorf("non-deterministic: %q != %q", m1, m2)
		}
	})

	t.Run("preserve suffix from NodeClaim name", func(t *testing.T) {
		name := "extremely-long-nodepool-name-that-definitely-exceeds-limits-xyz7890"
		m, err := GetAKSMachineNameFromNodeClaimName(name)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(m, "-xyz7890") {
			t.Errorf("machineName %q does not have suffix %q", m, "-xyz7890")
		}
	})

	t.Run("complex cases", func(t *testing.T) {
		cases := []struct {
			input      string
			wantExact  string
			wantLen    int
			wantPrefix string
			wantSuffix string
		}{
			{input: "a-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a1b2c", wantExact: "a-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a1b2c"},
			{input: "a-a-a-a-a-a-a-a-a-a-a-a-a-a-a--a1b2c", wantLen: 35, wantPrefix: "a-a-a-a-a-a-a-a-a-a-a-a", wantSuffix: "-a1b2c"},
			{input: "-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a-a1b2c", wantLen: 35, wantPrefix: "-a-a-a-a-a-a-a-a-a-a-a-", wantSuffix: "-a1b2c"},
			{input: "-a-a-a-a-a-a-a-a-a-a-a-a-a-a--a1b2c", wantExact: "-a-a-a-a-a-a-a-a-a-a-a-a-a-a--a1b2c"},
			{input: "------------------------------a1b2c", wantExact: "------------------------------a1b2c"},
			{input: "-------------------------------a1b2c", wantLen: 35, wantPrefix: "-----------------------", wantSuffix: "-a1b2c"},
		}
		for _, c := range cases {
			m, err := GetAKSMachineNameFromNodeClaimName(c.input)
			if err != nil {
				t.Fatalf("input %q: unexpected error: %v", c.input, err)
			}
			if c.wantExact != "" {
				if m != c.wantExact {
					t.Errorf("input %q: got %q, want %q", c.input, m, c.wantExact)
				}
				continue
			}
			if c.wantLen > 0 && len(m) != c.wantLen {
				t.Errorf("input %q: len = %d, want %d", c.input, len(m), c.wantLen)
			}
			if c.wantPrefix != "" && !strings.HasPrefix(m, c.wantPrefix) {
				t.Errorf("input %q: %q missing prefix %q", c.input, m, c.wantPrefix)
			}
			if c.wantSuffix != "" && !strings.HasSuffix(m, c.wantSuffix) {
				t.Errorf("input %q: %q missing suffix %q", c.input, m, c.wantSuffix)
			}
		}
	})
}

func TestGetAKSMachineNameFromNodeClaim(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantMachine string
		wantIsAKS   bool
	}{
		{
			name: "annotation exists",
			annotations: map[string]string{
				v1beta1.AnnotationAKSMachineResourceID: "/subscriptions/test/resourceGroups/test/providers/Microsoft.ContainerService/managedClusters/test/agentPools/aksmanagedap/machines/default-a1b2c3",
			},
			wantMachine: "default-a1b2c3",
			wantIsAKS:   true,
		},
		{
			name:        "annotation missing",
			annotations: map[string]string{},
			wantIsAKS:   false,
		},
		{
			name:        "annotations nil",
			annotations: nil,
			wantIsAKS:   false,
		},
		{
			name: "different format resource ID",
			annotations: map[string]string{
				v1beta1.AnnotationAKSMachineResourceID: "/different/path/to/machine-name-123",
			},
			wantMachine: "machine-name-123",
			wantIsAKS:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClaim := &karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			machineName, isAKSMachine := GetAKSMachineNameFromNodeClaim(nodeClaim)
			if isAKSMachine != tt.wantIsAKS {
				t.Errorf("isAKSMachine = %v, want %v", isAKSMachine, tt.wantIsAKS)
			}
			if tt.wantIsAKS && machineName != tt.wantMachine {
				t.Errorf("machineName = %q, want %q", machineName, tt.wantMachine)
			}
		})
	}
}

func TestGetCapacityTypeFromAKSScaleSetPriority(t *testing.T) {
	tests := []struct {
		name     string
		priority armcontainerservice.ScaleSetPriority
		want     string
	}{
		{
			name:     "spot priority",
			priority: armcontainerservice.ScaleSetPrioritySpot,
			want:     karpv1.CapacityTypeSpot,
		},
		{
			name:     "regular priority",
			priority: armcontainerservice.ScaleSetPriorityRegular,
			want:     karpv1.CapacityTypeOnDemand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getCapacityTypeFromAKSScaleSetPriority(tt.priority)
			if got != tt.want {
				t.Errorf("getCapacityTypeFromAKSScaleSetPriority = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetAKSMachineNameFromVMName(t *testing.T) {
	tests := []struct {
		name        string
		poolName    string
		vmName      string
		wantMachine string
		wantErr     bool
		errSubstr   string
	}{
		{
			name:        "valid VM name",
			poolName:    "aksmanagedap",
			vmName:      "aks-aksmanagedap-some-nodepool-a1b2c-12345678-vm",
			wantMachine: "some-nodepool-a1b2c",
		},
		{
			name:        "complex machine names with multiple dashes",
			poolName:    "aksmanagedap-aks-nodepool-abcde-12345678",
			vmName:      "aks-aksmanagedap-aks-nodepool-abcde-12345678-my-complex-machine-name-87654321-vm",
			wantMachine: "my-complex-machine-name",
		},
		{
			name:      "invalid prefix",
			poolName:  "machines",
			vmName:    "invalid-prefix-test-machine-123-12345678-vm",
			wantErr:   true,
			errSubstr: "does not start with expected prefix",
		},
		{
			name:      "insufficient parts",
			poolName:  "machines",
			vmName:    "aks-machines-12345678-vm",
			wantErr:   true,
			errSubstr: "does not have enough parts",
		},
		{
			name:      "invalid suffix",
			poolName:  "machines",
			vmName:    "aks-machines-test-machine-123-12345678-invalid",
			wantErr:   true,
			errSubstr: "does not end with expected suffix",
		},
		{
			name:      "unimplemented VMS suffix",
			poolName:  "pool1",
			vmName:    "aks-pool1-machine-name-87654321-vm99",
			wantErr:   true,
			errSubstr: "does not end with expected suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machineName, err := GetAKSMachineNameFromVMName(tt.poolName, tt.vmName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if machineName != tt.wantMachine {
				t.Errorf("machineName = %q, want %q", machineName, tt.wantMachine)
			}
		})
	}
}

func TestIsAKSMachineDeleting(t *testing.T) {
	tests := []struct {
		name    string
		machine *armcontainerservice.Machine
		want    bool
	}{
		{
			name: "Deleting state",
			machine: &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr("Deleting"),
				},
			},
			want: true,
		},
		{
			name: "Running state",
			machine: &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr("Running"),
				},
			},
			want: false,
		},
		{
			name:    "nil machine",
			machine: nil,
			want:    false,
		},
		{
			name: "nil properties",
			machine: &armcontainerservice.Machine{
				Properties: nil,
			},
			want: false,
		},
		{
			name: "nil provisioning state",
			machine: &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: nil,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAKSMachineDeleting(tt.machine)
			if got != tt.want {
				t.Errorf("isAKSMachineDeleting = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAKSLabelZoneFromAKSMachine(t *testing.T) {
	tests := []struct {
		name      string
		machine   *armcontainerservice.Machine
		location  string
		wantZone  string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "single zone",
			machine: &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("1")},
			},
			location: "eastus",
			wantZone: "eastus-1",
		},
		{
			name: "no zones - empty slice",
			machine: &armcontainerservice.Machine{
				Zones: []*string{},
			},
			location: "westus2",
			wantZone: "",
		},
		{
			name: "nil zones",
			machine: &armcontainerservice.Machine{
				Zones: nil,
			},
			location: "centralus",
			wantZone: "",
		},
		{
			name:      "nil machine",
			machine:   nil,
			location:  "eastus",
			wantErr:   true,
			errSubstr: "cannot pass in a nil AKS machine",
		},
		{
			name: "empty location",
			machine: &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("2")},
			},
			location:  "",
			wantErr:   true,
			errSubstr: "AKS machine is missing location",
		},
		{
			name: "multiple zones",
			machine: &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("1"), lo.ToPtr("2")},
			},
			location:  "eastus",
			wantErr:   true,
			errSubstr: "AKS machine has multiple zones",
		},
		{
			name: "zone 2 in westus2",
			machine: &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("2")},
			},
			location: "westus2",
			wantZone: "westus2-2",
		},
		{
			name: "zone 3 in centralus",
			machine: &armcontainerservice.Machine{
				Zones: []*string{lo.ToPtr("3")},
			},
			location: "centralus",
			wantZone: "centralus-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zone, err := GetAKSLabelZoneFromAKSMachine(tt.machine, tt.location)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if zone != tt.wantZone {
				t.Errorf("zone = %q, want %q", zone, tt.wantZone)
			}
		})
	}
}

func TestIsAKSMachineOrMachinesPoolNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "HTTP 404",
			err: &azcore.ResponseError{
				ErrorCode:   "lol",
				StatusCode:  404,
				RawResponse: nil,
			},
			want: true,
		},
		{
			name: "InvalidParameter with Cannot find valid machines message",
			err: createTestAzureResponseError(
				"InvalidParameter",
				"Cannot find any valid machines to delete. Please check your input machine names. The valid machines to delete in agent pool 'testmpool' are: testmachine.",
				400,
			),
			want: true,
		},
		{
			name: "InvalidParameter with different message",
			err: createTestAzureResponseError(
				"InvalidParameter",
				"InvalidParameter: Some other validation error",
				400,
			),
			want: false,
		},
		{
			name: "HTTP 401",
			err: &azcore.ResponseError{
				ErrorCode:   "Unauthorized",
				StatusCode:  401,
				RawResponse: nil,
			},
			want: false,
		},
		{
			name: "HTTP 500",
			err: &azcore.ResponseError{
				ErrorCode:   "InternalOperationError",
				StatusCode:  500,
				RawResponse: nil,
			},
			want: false,
		},
		{
			name: "non-Azure SDK error",
			err:  fmt.Errorf("some generic error"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAKSMachineOrMachinesPoolNotFound(tt.err)
			if got != tt.want {
				t.Errorf("IsAKSMachineOrMachinesPoolNotFound = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildJSONFromAKSMachine(t *testing.T) {
	tests := []struct {
		name         string
		machine      *armcontainerservice.Machine
		wantContains []string
		wantExact    string
	}{
		{
			name:      "nil machine",
			machine:   nil,
			wantExact: "{}",
		},
		{
			name: "minimal machine",
			machine: &armcontainerservice.Machine{
				Name: lo.ToPtr("test-machine"),
			},
			wantContains: []string{`"name":"test-machine"`},
		},
		{
			name: "fully populated machine",
			machine: &armcontainerservice.Machine{
				Name:  lo.ToPtr("test-machine"),
				Zones: []*string{lo.ToPtr("1")},
				Properties: &armcontainerservice.MachineProperties{
					NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-2023.11.15"),
					Hardware: &armcontainerservice.MachineHardwareProfile{
						VMSize: lo.ToPtr("Standard_D2s_v3"),
					},
					OperatingSystem: &armcontainerservice.MachineOSProfile{
						OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
						OSSKU:        lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
						OSDiskSizeGB: lo.ToPtr(int32(128)),
						OSDiskType:   lo.ToPtr(armcontainerservice.OSDiskTypeManaged),
						EnableFIPS:   lo.ToPtr(false),
					},
					Kubernetes: &armcontainerservice.MachineKubernetesProfile{
						OrchestratorVersion:      lo.ToPtr("1.29.0"),
						MaxPods:                  lo.ToPtr(int32(30)),
						NodeLabels:               map[string]*string{"env": lo.ToPtr("test")},
						NodeInitializationTaints: []*string{lo.ToPtr("node.kubernetes.io/not-ready:NoSchedule")},
					},
					Mode:     lo.ToPtr(armcontainerservice.AgentPoolModeUser),
					Priority: lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
					Security: &armcontainerservice.MachineSecurityProfile{
						EnableEncryptionAtHost: lo.ToPtr(false),
					},
					Tags: map[string]*string{
						"karpenter.sh/nodepool": lo.ToPtr("default"),
					},
				},
			},
			wantContains: []string{
				`"name":"test-machine"`,
				`"zones":["1"]`,
				`"nodeImageVersion":"AKSUbuntu-2204gen2containerd-2023.11.15"`,
				`"vmSize":"Standard_D2s_v3"`,
				`"osType":"Linux"`,
				`"osSKU":"Ubuntu2204"`,
				`"orchestratorVersion":"1.29.0"`,
				`"maxPods":30`,
				`"nodeLabels":{"env":"test"}`,
				`"nodeInitializationTaints":["node.kubernetes.io/not-ready:NoSchedule"]`,
				`"mode":"User"`,
				`"priority":"Regular"`,
			},
		},
		{
			name: "machine with empty properties",
			machine: &armcontainerservice.Machine{
				Name:       lo.ToPtr("test-machine"),
				Properties: &armcontainerservice.MachineProperties{},
			},
			wantContains: []string{`"name":"test-machine"`, `"properties":{}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildJSONFromAKSMachine(tt.machine)
			if tt.wantExact != "" {
				if result != tt.wantExact {
					t.Errorf("result = %q, want %q", result, tt.wantExact)
				}
				return
			}
			for _, s := range tt.wantContains {
				if !strings.Contains(result, s) {
					t.Errorf("result does not contain %q; got %q", s, result)
				}
			}
		})
	}
}
