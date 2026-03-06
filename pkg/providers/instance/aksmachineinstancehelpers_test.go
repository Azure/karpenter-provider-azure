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
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

func TestConfigureOSSKUAndFIPs(t *testing.T) {
	tests := []struct {
		name       string
		nodeClass  *v1beta1.AKSNodeClass
		k8sVersion string
		wantOSSKU  armcontainerservice.OSSKU
		wantFIPs   bool
		wantErr    bool
		errSubstr  string
	}{
		{
			name: "Ubuntu2204",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
				},
			},
			k8sVersion: "1.28.0",
			wantOSSKU:  armcontainerservice.OSSKUUbuntu2204,
			wantFIPs:   false,
		},
		{
			name: "Ubuntu2204 with different k8s version",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
				},
			},
			k8sVersion: "1.29.0",
			wantOSSKU:  armcontainerservice.OSSKUUbuntu2204,
			wantFIPs:   false,
		},
		{
			name: "AzureLinux for older k8s version",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.AzureLinuxImageFamily),
				},
			},
			k8sVersion: "1.28.0",
			wantOSSKU:  armcontainerservice.OSSKUAzureLinux,
			wantFIPs:   false,
		},
		{
			name: "AzureLinux for newer k8s version",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.AzureLinuxImageFamily),
				},
			},
			k8sVersion: "1.32.0",
			wantOSSKU:  armcontainerservice.OSSKUAzureLinux,
			wantFIPs:   false,
		},
		{
			name: "AzureLinux with FIPS mode",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.AzureLinuxImageFamily),
					FIPSMode:    lo.ToPtr(v1beta1.FIPSModeFIPS),
				},
			},
			k8sVersion: "1.28.0",
			wantOSSKU:  armcontainerservice.OSSKUAzureLinux,
			wantFIPs:   true,
		},
		{
			name: "Generic Ubuntu with FIPS mode",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.UbuntuImageFamily),
					FIPSMode:    lo.ToPtr(v1beta1.FIPSModeFIPS),
				},
			},
			k8sVersion: "1.28.0",
			wantOSSKU:  armcontainerservice.OSSKUUbuntu,
			wantFIPs:   true,
		},
		{
			name: "Generic Ubuntu without FIPS mode",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.UbuntuImageFamily),
				},
			},
			k8sVersion: "1.28.0",
			wantOSSKU:  armcontainerservice.OSSKUUbuntu2204,
			wantFIPs:   false,
		},
		{
			name: "Generic Ubuntu 2404 for newer k8s version",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.UbuntuImageFamily),
				},
			},
			k8sVersion: "1.34.0",
			wantOSSKU:  armcontainerservice.OSSKUUbuntu2404,
			wantFIPs:   false,
		},
		{
			name: "error when ImageFamily is nil",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: nil,
				},
			},
			k8sVersion: "1.28.0",
			wantErr:    true,
			errSubstr:  "ImageFamily is not set",
		},
		{
			name: "empty k8s version with AzureLinux",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: lo.ToPtr(v1beta1.AzureLinuxImageFamily),
				},
			},
			k8sVersion: "",
			wantOSSKU:  armcontainerservice.OSSKUAzureLinux,
			wantFIPs:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ossku, enableFIPs, err := configureOSSKUAndFIPs(tt.nodeClass, tt.k8sVersion)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ossku == nil || *ossku != tt.wantOSSKU {
				t.Errorf("ossku = %v, want %v", ossku, tt.wantOSSKU)
			}
			if enableFIPs == nil || *enableFIPs != tt.wantFIPs {
				t.Errorf("enableFIPs = %v, want %v", enableFIPs, tt.wantFIPs)
			}
		})
	}
}

//nolint:gocyclo
func TestConfigureTaints(t *testing.T) {
	tests := []struct {
		name                    string
		taints                  []v1.Taint
		startupTaints           []v1.Taint
		wantInitTaintsLen       int
		wantNodeTaintsLen       int
		wantUnregisteredCount   int
		checkContainsStrings    []string
		checkNotContainsStrings []string
	}{
		{
			name: "basic startup and regular taints",
			taints: []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
			},
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen:     3, // startup-taint + test-taint + UnregisteredNoExecuteTaint
			wantNodeTaintsLen:     0,
			wantUnregisteredCount: 1,
			checkContainsStrings:  []string{"startup-taint=startup-value:NoExecute", "test-taint=test-value:NoSchedule"},
		},
		{
			name: "no duplicate UnregisteredNoExecuteTaint if already in startup taints",
			taints: []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
			},
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
				karpv1.UnregisteredNoExecuteTaint,
			},
			wantInitTaintsLen:     3,
			wantNodeTaintsLen:     0,
			wantUnregisteredCount: 1,
		},
		{
			name: "no duplicate UnregisteredNoExecuteTaint if already in regular taints",
			taints: []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
				karpv1.UnregisteredNoExecuteTaint,
			},
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen:     3,
			wantNodeTaintsLen:     0,
			wantUnregisteredCount: 1,
		},
		{
			name:                  "empty taints only adds UnregisteredNoExecuteTaint",
			taints:                nil,
			startupTaints:         nil,
			wantInitTaintsLen:     1,
			wantNodeTaintsLen:     0,
			wantUnregisteredCount: 1,
		},
		{
			name: "empty startup taints but regular taints present",
			taints: []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
			},
			startupTaints:     nil,
			wantInitTaintsLen: 2, // test-taint + UnregisteredNoExecuteTaint
			wantNodeTaintsLen: 0,
		},
		{
			name:   "empty regular taints but startup taints present",
			taints: nil,
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen: 2, // startup-taint + UnregisteredNoExecuteTaint
			wantNodeTaintsLen: 0,
		},
		{
			name: "multiple startup taints",
			taints: []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
			},
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
				{Key: "another-startup", Value: "value", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen: 4, // 2 startup + test-taint + UnregisteredNoExecuteTaint
			wantNodeTaintsLen: 0,
		},
		{
			name: "multiple regular taints",
			taints: []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
				{Key: "another-regular", Value: "value", Effect: v1.TaintEffectNoSchedule},
			},
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen: 4, // startup + 2 regular + UnregisteredNoExecuteTaint
			wantNodeTaintsLen: 0,
		},
		{
			name: "taints with different effects",
			taints: []v1.Taint{
				{Key: "taint1", Value: "value1", Effect: v1.TaintEffectNoSchedule},
				{Key: "taint2", Value: "value2", Effect: v1.TaintEffectNoExecute},
				{Key: "taint3", Value: "value3", Effect: v1.TaintEffectPreferNoSchedule},
			},
			startupTaints: []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen:    5, // startup + 3 regular + UnregisteredNoExecuteTaint
			wantNodeTaintsLen:    0,
			checkContainsStrings: []string{"taint1=value1:NoSchedule", "taint2=value2:NoExecute", "taint3=value3:PreferNoSchedule"},
		},
		{
			name: "taints with empty values",
			taints: []v1.Taint{
				{Key: "empty-value-taint", Value: "", Effect: v1.TaintEffectNoSchedule},
			},
			startupTaints: []v1.Taint{
				{Key: "empty-startup-taint", Value: "", Effect: v1.TaintEffectNoExecute},
			},
			wantInitTaintsLen:    3, // empty-startup + empty-value + UnregisteredNoExecuteTaint
			wantNodeTaintsLen:    0,
			checkContainsStrings: []string{"empty-value-taint:NoSchedule", "empty-startup-taint:NoExecute"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClaim := &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Taints:        tt.taints,
					StartupTaints: tt.startupTaints,
				},
			}

			initTaints, nodeTaints := configureTaints(nodeClaim)

			if len(initTaints) != tt.wantInitTaintsLen {
				t.Errorf("initTaints length = %d, want %d", len(initTaints), tt.wantInitTaintsLen)
			}
			if len(nodeTaints) != tt.wantNodeTaintsLen {
				t.Errorf("nodeTaints length = %d, want %d", len(nodeTaints), tt.wantNodeTaintsLen)
			}

			// Check UnregisteredNoExecuteTaint count
			if tt.wantUnregisteredCount > 0 {
				count := 0
				for _, taint := range initTaints {
					if *taint == karpv1.UnregisteredNoExecuteTaint.ToString() {
						count++
					}
				}
				if count != tt.wantUnregisteredCount {
					t.Errorf("UnregisteredNoExecuteTaint count = %d, want %d", count, tt.wantUnregisteredCount)
				}
			}

			// Check taint string containment
			if len(tt.checkContainsStrings) > 0 {
				taintStrings := make([]string, len(initTaints))
				for i, taint := range initTaints {
					taintStrings[i] = *taint
				}
				for _, want := range tt.checkContainsStrings {
					found := false
					for _, got := range taintStrings {
						if got == want {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("initTaints does not contain %q; got %v", want, taintStrings)
					}
				}
			}
		})
	}
}

//nolint:gocyclo
func TestConfigureLabelsAndMode(t *testing.T) {
	instanceType := &corecloudprovider.InstanceType{
		Name: "Standard_D2_v2",
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
		),
	}

	tests := []struct {
		name              string
		labels            map[string]string
		capacityType      string
		wantMode          armcontainerservice.AgentPoolMode
		wantCapacityType  string
		checkLabelKeys    []string
		checkLabelValues  map[string]string
		complexReqs       bool
		nilLabels         bool
		emptyRequirements bool
	}{
		{
			name:             "user mode by default",
			labels:           map[string]string{"test-label": "test-value"},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
		},
		{
			name:             "system mode when explicitly specified",
			labels:           map[string]string{"test-label": "test-value", "kubernetes.azure.com/mode": "system"},
			capacityType:     karpv1.CapacityTypeSpot,
			wantMode:         armcontainerservice.AgentPoolModeSystem,
			wantCapacityType: karpv1.CapacityTypeSpot,
		},
		{
			name:             "user mode when mode label is user",
			labels:           map[string]string{"test-label": "test-value", "kubernetes.azure.com/mode": "user"},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
		},
		{
			name:             "user mode when mode label is empty",
			labels:           map[string]string{"test-label": "test-value", "kubernetes.azure.com/mode": ""},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
		},
		{
			name:             "user mode when mode label has invalid value",
			labels:           map[string]string{"test-label": "test-value", "kubernetes.azure.com/mode": "invalid-mode"},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
		},
		{
			name:             "on-demand capacity type",
			labels:           map[string]string{"test-label": "test-value"},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
		},
		{
			name:             "spot capacity type",
			labels:           map[string]string{"test-label": "test-value"},
			capacityType:     karpv1.CapacityTypeSpot,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeSpot,
		},
		{
			name:             "include original nodeclaim labels",
			labels:           map[string]string{"custom-label": "custom-value", "another-label": "another-value", "environment": "test"},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
			checkLabelValues: map[string]string{"custom-label": "custom-value", "another-label": "another-value", "environment": "test"},
		},
		{
			name:             "nil nodeclaim labels",
			nilLabels:        true,
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
		},
		{
			name:              "empty instance type requirements",
			labels:            map[string]string{"test-label": "test-value"},
			capacityType:      karpv1.CapacityTypeOnDemand,
			wantMode:          armcontainerservice.AgentPoolModeUser,
			wantCapacityType:  karpv1.CapacityTypeOnDemand,
			emptyRequirements: true,
			checkLabelValues:  map[string]string{"test-label": "test-value"},
		},
		{
			name:             "complex instance type requirements",
			labels:           map[string]string{"test-label": "test-value"},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
			complexReqs:      true,
			checkLabelValues: map[string]string{"custom-requirement": "custom-value"},
		},
		{
			name:             "very long label values",
			labels:           map[string]string{"long-label": strings.Repeat("a", 1000)},
			capacityType:     karpv1.CapacityTypeOnDemand,
			wantMode:         armcontainerservice.AgentPoolModeUser,
			wantCapacityType: karpv1.CapacityTypeOnDemand,
			checkLabelValues: map[string]string{"long-label": strings.Repeat("a", 1000)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClaim := &karpv1.NodeClaim{}
			if !tt.nilLabels {
				nodeClaim.Labels = tt.labels
			}
			nodeClaim.Spec.Taints = []v1.Taint{
				{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
			}
			nodeClaim.Spec.StartupTaints = []v1.Taint{
				{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
			}

			it := instanceType
			if tt.emptyRequirements {
				it = &corecloudprovider.InstanceType{
					Name:         "Standard_D2_v2",
					Requirements: scheduling.NewRequirements(),
				}
			}
			if tt.complexReqs {
				it = &corecloudprovider.InstanceType{
					Name: "Standard_D2_v2",
					Requirements: scheduling.NewRequirements(
						scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
						scheduling.NewRequirement(v1.LabelInstanceTypeStable, v1.NodeSelectorOpIn, "Standard_D2_v2"),
						scheduling.NewRequirement(v1.LabelTopologyZone, v1.NodeSelectorOpIn, "eastus-1"),
						scheduling.NewRequirement("custom-requirement", v1.NodeSelectorOpIn, "custom-value"),
					),
				}
			}

			labels, mode := configureLabelsAndMode(nodeClaim, it, tt.capacityType)

			if labels == nil {
				t.Fatal("labels is nil, expected non-nil")
			}
			if mode == nil || *mode != tt.wantMode {
				t.Errorf("mode = %v, want %v", mode, tt.wantMode)
			}
			capLabel, ok := labels[karpv1.CapacityTypeLabelKey]
			if !ok {
				t.Fatal("capacity type label not found")
			}
			if *capLabel != tt.wantCapacityType {
				t.Errorf("capacity type = %q, want %q", *capLabel, tt.wantCapacityType)
			}

			for k, v := range tt.checkLabelValues {
				labelVal, ok := labels[k]
				if !ok {
					t.Errorf("label %q not found in labels", k)
					continue
				}
				if *labelVal != v {
					t.Errorf("label %q = %q, want %q", k, *labelVal, v)
				}
			}
		})
	}

	// Separate test for label ordering consistency
	t.Run("preserve label ordering consistency", func(t *testing.T) {
		nodeClaim := &karpv1.NodeClaim{}
		nodeClaim.Labels = map[string]string{
			"z-label": "z-value",
			"a-label": "a-value",
			"m-label": "m-value",
		}
		labels1, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)
		labels2, _ := configureLabelsAndMode(nodeClaim, instanceType, karpv1.CapacityTypeOnDemand)

		if len(labels1) != len(labels2) {
			t.Fatalf("labels1 length %d != labels2 length %d", len(labels1), len(labels2))
		}
		for key := range labels1 {
			v2, ok := labels2[key]
			if !ok {
				t.Errorf("key %q in labels1 but not in labels2", key)
				continue
			}
			if *labels1[key] != *v2 {
				t.Errorf("labels1[%q] = %q, labels2[%q] = %q", key, *labels1[key], key, *v2)
			}
		}
	})
}

//nolint:gocyclo
func TestConfigureKubeletConfig(t *testing.T) {
	tests := []struct {
		name      string
		nodeClass *v1beta1.AKSNodeClass
		validate  func(t *testing.T, config *armcontainerservice.KubeletConfig)
	}{
		{
			name:      "nil nodeClass returns nil",
			nodeClass: nil,
			validate: func(t *testing.T, config *armcontainerservice.KubeletConfig) {
				if config != nil {
					t.Errorf("expected nil config, got %v", config)
				}
			},
		},
		{
			name: "nil kubelet spec returns nil",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					Kubelet: nil,
				},
			},
			validate: func(t *testing.T, config *armcontainerservice.KubeletConfig) {
				if config != nil {
					t.Errorf("expected nil config, got %v", config)
				}
			},
		},
		{
			name: "all kubelet settings configured",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					Kubelet: &v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            lo.ToPtr("static"),
						CPUCFSQuota:                 lo.ToPtr(true),
						TopologyManagerPolicy:       lo.ToPtr("single-numa-node"),
						ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
						ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
						AllowedUnsafeSysctls:        []string{"kernel.shm_rmid_forced", "net.core.somaxconn"},
						ContainerLogMaxSize:         lo.ToPtr("100Mi"),
						ContainerLogMaxFiles:        lo.ToPtr(int32(5)),
						PodPidsLimit:                lo.ToPtr(int64(2048)),
					},
				},
			},
			validate: func(t *testing.T, config *armcontainerservice.KubeletConfig) {
				if config == nil {
					t.Fatal("config is nil")
					return
				}
				if *config.CPUManagerPolicy != "static" {
					t.Errorf("CPUManagerPolicy = %q, want %q", *config.CPUManagerPolicy, "static")
				}
				if *config.CPUCfsQuota != true {
					t.Errorf("CPUCfsQuota = %v, want true", *config.CPUCfsQuota)
				}
				if *config.TopologyManagerPolicy != "single-numa-node" {
					t.Errorf("TopologyManagerPolicy = %q, want %q", *config.TopologyManagerPolicy, "single-numa-node")
				}
				if *config.ImageGcHighThreshold != int32(85) {
					t.Errorf("ImageGcHighThreshold = %d, want 85", *config.ImageGcHighThreshold)
				}
				if *config.ImageGcLowThreshold != int32(80) {
					t.Errorf("ImageGcLowThreshold = %d, want 80", *config.ImageGcLowThreshold)
				}
				if len(config.AllowedUnsafeSysctls) != 2 {
					t.Errorf("AllowedUnsafeSysctls length = %d, want 2", len(config.AllowedUnsafeSysctls))
				}
				if *config.AllowedUnsafeSysctls[0] != "kernel.shm_rmid_forced" {
					t.Errorf("AllowedUnsafeSysctls[0] = %q, want %q", *config.AllowedUnsafeSysctls[0], "kernel.shm_rmid_forced")
				}
				if config.ContainerLogMaxSizeMB == nil {
					t.Error("ContainerLogMaxSizeMB is nil")
				}
				if *config.ContainerLogMaxFiles != int32(5) {
					t.Errorf("ContainerLogMaxFiles = %d, want 5", *config.ContainerLogMaxFiles)
				}
				if config.PodMaxPids == nil {
					t.Error("PodMaxPids is nil")
				}
			},
		},
		{
			name: "empty/nil values handled correctly",
			nodeClass: &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					Kubelet: &v1beta1.KubeletConfiguration{
						CPUManagerPolicy:     lo.ToPtr(""),
						CPUCFSQuota:          lo.ToPtr(false),
						AllowedUnsafeSysctls: []string{},
						ContainerLogMaxSize:  nil,
						ContainerLogMaxFiles: nil,
						PodPidsLimit:         nil,
					},
				},
			},
			validate: func(t *testing.T, config *armcontainerservice.KubeletConfig) {
				if config.CPUManagerPolicy != nil {
					t.Errorf("CPUManagerPolicy = %v, want nil", config.CPUManagerPolicy)
				}
				if *config.CPUCfsQuota != false {
					t.Errorf("CPUCfsQuota = %v, want false", *config.CPUCfsQuota)
				}
				if config.AllowedUnsafeSysctls != nil {
					t.Errorf("AllowedUnsafeSysctls = %v, want nil", config.AllowedUnsafeSysctls)
				}
				if config.ContainerLogMaxSizeMB != nil {
					t.Errorf("ContainerLogMaxSizeMB = %v, want nil", config.ContainerLogMaxSizeMB)
				}
				if config.ContainerLogMaxFiles != nil {
					t.Errorf("ContainerLogMaxFiles = %v, want nil", config.ContainerLogMaxFiles)
				}
				if config.PodMaxPids != nil {
					t.Errorf("PodMaxPids = %v, want nil", config.PodMaxPids)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := configureKubeletConfig(tt.nodeClass)
			tt.validate(t, config)
		})
	}
}

//nolint:gocyclo
func TestParseVMImageID(t *testing.T) {
	tests := []struct {
		name              string
		vmImageID         string
		wantErr           bool
		errSubstr         string
		wantSubscription  string
		wantResourceGroup string
		wantGallery       string
		wantImageName     string
		wantVersion       string
	}{
		{
			name:              "complete VM image ID",
			vmImageID:         "/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantSubscription:  "10945678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
		{
			name:              "VM image ID with different values",
			vmImageID:         "/subscriptions/abcdef12-3456-7890-abcd-ef1234567890/resourceGroups/MyResourceGroup/providers/Microsoft.Compute/galleries/MyGallery/images/ubuntu20-04/versions/1.0.0",
			wantSubscription:  "abcdef12-3456-7890-abcd-ef1234567890",
			wantResourceGroup: "MyResourceGroup",
			wantGallery:       "MyGallery",
			wantImageName:     "ubuntu20-04",
			wantVersion:       "1.0.0",
		},
		{
			name:              "image ID with hyphens and underscores in names",
			vmImageID:         "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg_name/providers/Microsoft.Compute/galleries/test_gallery-name/images/test-image_name/versions/2023.01.15",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "test-rg_name",
			wantGallery:       "test_gallery-name",
			wantImageName:     "test-image_name",
			wantVersion:       "2023.01.15",
		},
		{
			name:      "empty string",
			vmImageID: "",
			wantErr:   true,
			errSubstr: "vmImageID is empty",
		},
		{
			name:      "too few path components",
			vmImageID: "/subscriptions/12345/resourceGroups/test",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:      "wrong subscriptions keyword",
			vmImageID: "/subscription/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantErr:   true,
			errSubstr: "invalid vmImageID format",
		},
		{
			name:      "wrong resourceGroups keyword",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroup/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantErr:   true,
			errSubstr: "missing resource group",
		},
		{
			name:      "wrong providers keyword",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/provider/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:      "wrong Microsoft.Compute provider",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Storage/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:      "wrong galleries keyword",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/gallery/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:      "wrong images keyword",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/image/2204gen2containerd/versions/2022.10.03",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:      "wrong versions keyword",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/version/2022.10.03",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:      "missing version value",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions",
			wantErr:   true,
			errSubstr: "missing version",
		},
		{
			name:              "trailing slash handled gracefully",
			vmImageID:         "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03/",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
		{
			name:              "minimum required path length",
			vmImageID:         "/subscriptions/a/resourceGroups/b/providers/Microsoft.Compute/galleries/c/images/d/versions/e",
			wantSubscription:  "a",
			wantResourceGroup: "b",
			wantGallery:       "c",
			wantImageName:     "d",
			wantVersion:       "e",
		},
		{
			name:      "extra trailing components",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03/extra/components",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:              "uppercase SUBSCRIPTIONS",
			vmImageID:         "/SUBSCRIPTIONS/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
		{
			name:              "uppercase RESOURCEGROUPS",
			vmImageID:         "/subscriptions/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
		{
			name:      "single extra trailing component",
			vmImageID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03/extra",
			wantErr:   true,
			errSubstr: "expected resource type Microsoft.Compute/galleries/images/versions",
		},
		{
			name:              "uppercase MICROSOFT.COMPUTE",
			vmImageID:         "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/MICROSOFT.COMPUTE/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
		{
			name:              "mixed case GaLlErIeS",
			vmImageID:         "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/GaLlErIeS/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
		{
			name:              "fully uppercase path",
			vmImageID:         "/SUBSCRIPTIONS/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/AKS-Ubuntu/PROVIDERS/MICROSOFT.COMPUTE/GALLERIES/AKSUbuntu/IMAGES/2204gen2containerd/VERSIONS/2022.10.03",
			wantSubscription:  "12345678-1234-1234-1234-123456789012",
			wantResourceGroup: "AKS-Ubuntu",
			wantGallery:       "AKSUbuntu",
			wantImageName:     "2204gen2containerd",
			wantVersion:       "2022.10.03",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subscriptionID, resourceGroup, gallery, imageName, version, err := parseVMImageID(tt.vmImageID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if subscriptionID != tt.wantSubscription {
				t.Errorf("subscriptionID = %q, want %q", subscriptionID, tt.wantSubscription)
			}
			if resourceGroup != tt.wantResourceGroup {
				t.Errorf("resourceGroup = %q, want %q", resourceGroup, tt.wantResourceGroup)
			}
			if gallery != tt.wantGallery {
				t.Errorf("gallery = %q, want %q", gallery, tt.wantGallery)
			}
			if imageName != tt.wantImageName {
				t.Errorf("imageName = %q, want %q", imageName, tt.wantImageName)
			}
			if version != tt.wantVersion {
				t.Errorf("version = %q, want %q", version, tt.wantVersion)
			}
		})
	}
}
