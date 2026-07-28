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
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
)

func prepareTestKubeletConfiguration(enableNodeHardening bool) *bootstrap.KubeletConfiguration {
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
	ctx := options.ToContext(context.Background(), &options.Options{EnableNodeHardening: enableNodeHardening})
	return prepareKubeletConfiguration(ctx, instanceType, nodeClass)
}

func TestPrepareKubeletConfigurationSoftEvictionEnabled(t *testing.T) {
	configuration := prepareTestKubeletConfiguration(true)

	expectedThresholds := map[string]string{
		"memory.available":  "1Gi",
		"nodefs.available":  "12%",
		"nodefs.inodesFree": "7%",
	}
	if !reflect.DeepEqual(configuration.EvictionSoft, expectedThresholds) {
		t.Fatalf("soft eviction thresholds = %#v, want %#v", configuration.EvictionSoft, expectedThresholds)
	}

	expectedGracePeriods := map[string]metav1.Duration{
		"memory.available":  {Duration: 30 * time.Second},
		"nodefs.available":  {Duration: 2 * time.Minute},
		"nodefs.inodesFree": {Duration: 2 * time.Minute},
	}
	if !reflect.DeepEqual(configuration.EvictionSoftGracePeriod, expectedGracePeriods) {
		t.Fatalf("soft eviction grace periods = %#v, want %#v", configuration.EvictionSoftGracePeriod, expectedGracePeriods)
	}
	if configuration.EvictionMaxPodGracePeriod == nil || *configuration.EvictionMaxPodGracePeriod != 60 {
		t.Fatalf("max pod grace period = %v, want 60", configuration.EvictionMaxPodGracePeriod)
	}
	expectedEnforcement := []string{"pods", "kube-reserved", "system-reserved"}
	if !reflect.DeepEqual(configuration.EnforceNodeAllocatable, expectedEnforcement) {
		t.Fatalf("node allocatable enforcement = %#v, want %#v", configuration.EnforceNodeAllocatable, expectedEnforcement)
	}
	if configuration.SystemReserved["pid"] != "1000" {
		t.Fatalf("system reserved pid = %q, want %q", configuration.SystemReserved["pid"], "1000")
	}
}

func TestPrepareKubeletConfigurationSoftEvictionDisabled(t *testing.T) {
	configuration := prepareTestKubeletConfiguration(false)

	if configuration.EvictionSoft != nil {
		t.Fatalf("soft eviction thresholds = %#v, want nil", configuration.EvictionSoft)
	}
	if configuration.EvictionSoftGracePeriod != nil {
		t.Fatalf("soft eviction grace periods = %#v, want nil", configuration.EvictionSoftGracePeriod)
	}
	if configuration.EvictionMaxPodGracePeriod != nil {
		t.Fatalf("max pod grace period = %v, want nil", configuration.EvictionMaxPodGracePeriod)
	}
	if configuration.EnforceNodeAllocatable != nil {
		t.Fatalf("node allocatable enforcement = %#v, want nil", configuration.EnforceNodeAllocatable)
	}
	if _, ok := configuration.SystemReserved["pid"]; ok {
		t.Fatalf("system reserved pid should not be set when hardening is disabled")
	}
}
