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

package bootstrap

import (
	_ "embed"
	"encoding/base64"
	"testing"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
)

func TestGetSysctlContent(t *testing.T) {
	// TestGetSysctlContent tests the getSysctlContent function.
	type args struct {
		s *nbcontractv1.SysctlConfig
	}
	int9999 := int32(9999)
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Default SysctlConfig",
			args: args{
				s: &nbcontractv1.SysctlConfig{},
			},
			want: base64.StdEncoding.EncodeToString(
				[]byte("net.core.message_burst=80 net.core.message_cost=40 net.core.somaxconn=16384 net.ipv4.neigh.default.gc_thresh1=4096 net.ipv4.neigh.default.gc_thresh2=8192 net.ipv4.neigh.default.gc_thresh3=16384 net.ipv4.tcp_max_syn_backlog=16384 net.ipv4.tcp_retries2=8")), // Update with expected value
		},
		{
			name: "SysctlConfig with custom values",
			args: args{
				s: &nbcontractv1.SysctlConfig{
					NetIpv4TcpMaxSynBacklog: &int9999,
					NetCoreRmemDefault:      &int9999,
				},
			},
			want: base64.StdEncoding.EncodeToString(
				[]byte("net.core.message_burst=80 net.core.message_cost=40 net.core.rmem_default=9999 net.core.somaxconn=16384 net.ipv4.neigh.default.gc_thresh1=4096 net.ipv4.neigh.default.gc_thresh2=8192 net.ipv4.neigh.default.gc_thresh3=16384 net.ipv4.tcp_max_syn_backlog=9999 net.ipv4.tcp_retries2=8")), // Update with expected value
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getSysctlContent(tt.args.s); got != tt.want {
				t.Errorf("getSysctlContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getUlimitContent(t *testing.T) {
	type args struct {
		u *nbcontractv1.UlimitConfig
	}
	str9999 := "9999"
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Default UlimitConfig",
			args: args{
				u: &nbcontractv1.UlimitConfig{},
			},
			want: base64.StdEncoding.EncodeToString(
				[]byte("[Service]\n")),
		},
		{
			name: "UlimitConfig with custom values",
			args: args{
				u: &nbcontractv1.UlimitConfig{
					NoFile:          &str9999,
					MaxLockedMemory: &str9999,
				},
			},
			want: base64.StdEncoding.EncodeToString(
				[]byte("[Service]\nLimitMEMLOCK=9999 LimitNOFILE=9999")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getUlimitContent(tt.args.u); got != tt.want {
				t.Errorf("getUlimitContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getKubeletConfigFileEnabled(t *testing.T) {
	type args struct {
		configContent string
		k8sVersion    string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Kubelet config file enabled",
			args: args{
				configContent: "some config content",
				k8sVersion:    "1.20.0",
			},
			want: true,
		},
		{
			name: "Kubelet config file disabled",
			args: args{
				configContent: "",
				k8sVersion:    "1.20.0",
			},
			want: false,
		},
		{
			name: "Kubelet config file disabled for k8s version < 1.14.0",
			args: args{
				configContent: "some config content",
				k8sVersion:    "1.13.5",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getKubeletConfigFileEnabled(tt.args.configContent, tt.args.k8sVersion); got != tt.want {
				t.Errorf("getKubeletConfigFileEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_createSortedKeyValueStringPairs(t *testing.T) {
	type args struct {
		m         map[string]string
		delimiter string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Empty map",
			args: args{
				m:         map[string]string{},
				delimiter: ",",
			},
			want: "",
		},
		{
			name: "Single key-value pair",
			args: args{
				m:         map[string]string{"key1": "value1"},
				delimiter: " ",
			},
			want: "key1=value1",
		},
		{
			name: "Multiple key-value pairs with delimiter ,",
			args: args{
				m:         map[string]string{"key1": "value1", "key2": "value2"},
				delimiter: ",",
			},
			want: "key1=value1,key2=value2",
		},
		{
			name: "Multiple key-value pairs with delimiter space",
			args: args{
				m:         map[string]string{"key1": "value1", "key2": "value2"},
				delimiter: " ",
			},
			want: "key1=value1 key2=value2",
		},
		{
			name: "Sorting key-value pairs",
			args: args{
				m:         map[string]string{"b": "valb", "a": "vala", "c": "valc"},
				delimiter: ",",
			},
			want: "a=vala,b=valb,c=valc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := createSortedKeyValuePairs(tt.args.m, tt.args.delimiter); got != tt.want {
				t.Errorf("createSortedKeyValuePairs() with map[string]string = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_createSortedKeyValueInt32Pairs(t *testing.T) {
	type args struct {
		m         map[string]int32
		delimiter string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Empty map",
			args: args{
				m:         map[string]int32{},
				delimiter: ",",
			},
			want: "",
		},
		{
			name: "Single key-value pair",
			args: args{
				m:         map[string]int32{"key1": 1},
				delimiter: " ",
			},
			want: "key1=1",
		},
		{
			name: "Multiple key-value pairs",
			args: args{
				m:         map[string]int32{"key1": 1, "key2": 2},
				delimiter: ",",
			},
			want: "key1=1,key2=2",
		},
		{
			name: "Multiple key-value pairs with delimiter space",
			args: args{
				m:         map[string]int32{"key1": 1, "key2": 2},
				delimiter: " ",
			},
			want: "key1=1 key2=2",
		},
		{
			name: "Sorting key-value pairs",
			args: args{
				m:         map[string]int32{"b": 2, "a": 1, "c": 3},
				delimiter: ",",
			},
			want: "a=1,b=2,c=3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := createSortedKeyValuePairs(tt.args.m, tt.args.delimiter); got != tt.want {
				t.Errorf("createSortedKeyValuePairs() with map[string]int32 = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getContainerdConfig(t *testing.T) {
	type args struct {
		nbcontract *nbcontractv1.Configuration
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Default Configuration",
			args: args{
				nbcontract: &nbcontractv1.Configuration{
					NeedsCgroupv2: true,
				},
			},
			want: base64.StdEncoding.EncodeToString([]byte(`version = 2
oom_score = 0
[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "mcr.microsoft.com/oss/kubernetes/pause:3.6"
  [plugins."io.containerd.grpc.v1.cri".containerd]
    default_runtime_name = "runc"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
      runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
      BinaryName = "/usr/bin/runc"
      SystemdCgroup = true
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.untrusted]
      runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.untrusted.options]
      BinaryName = "/usr/bin/runc"
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
  [plugins."io.containerd.grpc.v1.cri".registry.headers]
    X-Meta-Source-Client = ["azure/aks"]
[metrics]
  address = "0.0.0.0:10257"
`)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getContainerdConfig(tt.args.nbcontract); got != tt.want {
				t.Errorf("getContainerdConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getKubenetTemplate(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "Kubenet template",
			want: base64.StdEncoding.EncodeToString([]byte(`{
	"cniVersion": "0.3.1",
	"name": "kubenet",
	"plugins": [{
		"type": "bridge",
		"bridge": "cbr0",
		"mtu": 1500,
		"addIf": "eth0",
		"isGateway": true,
		"ipMasq": false,
		"promiscMode": true,
		"hairpinMode": false,
		"ipam": {
			"type": "host-local",
			"ranges": [{{range $i, $range := .PodCIDRRanges}}{{if $i}}, {{end}}[{"subnet": "{{$range}}"}]{{end}}],
			"routes": [{{range $i, $route := .Routes}}{{if $i}}, {{end}}{"dst": "{{$route}}"}{{end}}]
		}
	},
	{
		"type": "portmap",
		"capabilities": {"portMappings": true},
		"externalSetMarkChain": "KUBE-MARK-MASQ"
	}]
}
`)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getKubenetTemplate(); got != tt.want {
				t.Errorf("getKubenetTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getAzureEnvironmentFilepath(t *testing.T) {
	type args struct {
		v *nbcontractv1.CustomCloudConfig
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Empty AzureEnvironmentFilepath",
			args: args{
				v: &nbcontractv1.CustomCloudConfig{},
			},
			want: "",
		},
		{
			name: "AzureEnvironmentFilepath when it is AKSCustomCloud",
			args: args{
				v: &nbcontractv1.CustomCloudConfig{
					IsAksCustomCloud:  true,
					TargetEnvironment: "testcloud",
				},
			},
			want: "/etc/kubernetes/testcloud.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getAzureEnvironmentFilepath(tt.args.v); got != tt.want {
				t.Errorf("getAzureEnvironmentFilepath() = %v, want %v", got, tt.want)
			}
		})
	}
}
