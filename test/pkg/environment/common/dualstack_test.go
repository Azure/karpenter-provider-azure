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

package common

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

func TestLinuxDualStackProbeOptions(t *testing.T) {
	options := linuxDualStackProbeOptions()
	for key, want := range map[string]string{
		corev1.LabelOSStable: string(corev1.Linux),
		v1beta1.AKSLabelMode: v1beta1.ModeSystem,
	} {
		if got := options.NodeSelector[key]; got != want {
			t.Errorf("NodeSelector[%q] = %q, want %q", key, got, want)
		}
	}

	if len(options.Tolerations) != 1 {
		t.Fatalf("len(Tolerations) = %d, want 1", len(options.Tolerations))
	}
	toleration := options.Tolerations[0]

	for _, tc := range []struct {
		name  string
		taint corev1.Taint
		want  bool
	}{
		{
			name: "valueless system pool taint",
			taint: corev1.Taint{
				Key:    "CriticalAddonsOnly",
				Effect: corev1.TaintEffectNoSchedule,
			},
			want: true,
		},
		{
			name: "valued system pool taint",
			taint: corev1.Taint{
				Key:    "CriticalAddonsOnly",
				Value:  "true",
				Effect: corev1.TaintEffectNoSchedule,
			},
			want: true,
		},
		{
			name: "unrelated no-schedule taint",
			taint: corev1.Taint{
				Key:    "unrelated",
				Effect: corev1.TaintEffectNoSchedule,
			},
			want: false,
		},
		{
			name: "system pool taint with unrelated effect",
			taint: corev1.Taint{
				Key:    "CriticalAddonsOnly",
				Effect: corev1.TaintEffectNoExecute,
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toleration.ToleratesTaint(klog.Background(), &tc.taint, false); got != tc.want {
				t.Errorf("ToleratesTaint(%+v) = %t, want %t", tc.taint, got, tc.want)
			}
		})
	}
}
