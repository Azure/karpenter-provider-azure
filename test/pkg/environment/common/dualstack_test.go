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

func TestHasIPv4AndIPv6PodIPs(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []corev1.PodIP
		want bool
	}{
		{name: "empty", ips: nil, want: false},
		{name: "IPv4 only", ips: []corev1.PodIP{{IP: "10.0.0.4"}}, want: false},
		{name: "IPv6 only", ips: []corev1.PodIP{{IP: "fd00::4"}}, want: false},
		{name: "invalid plus IPv6", ips: []corev1.PodIP{{IP: "not-an-ip"}, {IP: "fd00::4"}}, want: false},
		{name: "dual stack", ips: []corev1.PodIP{{IP: "10.0.0.4"}, {IP: "fd00::4"}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasIPv4AndIPv6PodIPs(tc.ips); got != tc.want {
				t.Fatalf("HasIPv4AndIPv6PodIPs(%v) = %v, want %v", tc.ips, got, tc.want)
			}
		})
	}
}

func TestHasIPv4AndIPv6NodeInternalIPs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []corev1.NodeAddress
		want      bool
	}{
		{name: "empty", addresses: nil, want: false},
		{name: "IPv4 only", addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}}, want: false},
		{name: "external IPv6 is ignored", addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}, {Type: corev1.NodeExternalIP, Address: "fd00::4"}}, want: false},
		{name: "dual stack", addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}, {Type: corev1.NodeInternalIP, Address: "fd00::4"}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &corev1.Node{Status: corev1.NodeStatus{Addresses: tc.addresses}}
			if got := HasIPv4AndIPv6NodeInternalIPs(node); got != tc.want {
				t.Fatalf("HasIPv4AndIPv6NodeInternalIPs(%v) = %v, want %v", tc.addresses, got, tc.want)
			}
		})
	}
}

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
