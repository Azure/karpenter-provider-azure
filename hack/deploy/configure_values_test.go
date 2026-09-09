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

package deploy_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestConfigureValues(t *testing.T) {
	type agentPool struct {
		VnetSubnetID        string  `json:"vnetSubnetId,omitempty"`
		PodSubnetID         string  `json:"podSubnetId,omitempty"`
		PodIPAllocationMode *string `json:"podIpAllocationMode"`
	}
	type networkSettings struct {
		VnetSubnetID        string `json:"VNET_SUBNET_ID"`
		PodSubnetID         string `json:"POD_SUBNET_ID"`
		PodIPAllocationMode string `json:"POD_IP_ALLOCATION_MODE"`
		VnetGUID            string `json:"VNET_GUID"`
	}
	type testCase struct {
		name    string
		mode    string
		pools   []agentPool
		want    networkSettings
		wantErr string
	}

	const vnetID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"
	const nodeSubnet = vnetID + "/subnets/nodes"
	const podSubnet = vnetID + "/subnets/pods"
	const ambiguous = "Agent pools have different node subnets, pod subnets, or pod IP allocation modes."
	dynamic, static, empty := "DynamicIndividual", "StaticBlock", ""
	lowerDynamic, lowerStatic := strings.ToLower(dynamic), strings.ToLower(static)
	dynamicPool := agentPool{nodeSubnet, podSubnet, &dynamic}
	staticPool := agentPool{nodeSubnet, podSubnet, &static}
	noPodPool := agentPool{VnetSubnetID: nodeSubnet}
	wantDynamic := networkSettings{nodeSubnet, podSubnet, dynamic, "fake-vnet-guid"}
	wantStatic := networkSettings{nodeSubnet, podSubnet, static, "fake-vnet-guid"}
	wantNoPod := networkSettings{VnetSubnetID: nodeSubnet, VnetGUID: "fake-vnet-guid"}
	tests := []testCase{
		{name: "default dynamic", pools: []agentPool{dynamicPool}, want: wantDynamic},
		{name: "default static", pools: []agentPool{staticPool}, want: wantStatic},
		{name: "explicit scriptless dynamic", mode: "aksscriptless", pools: []agentPool{dynamicPool}, want: wantDynamic},
		{name: "explicit scriptless static", mode: "aksscriptless", pools: []agentPool{staticPool}, want: wantStatic},
		{name: "null allocation mode", pools: []agentPool{{nodeSubnet, podSubnet, nil}}, want: wantDynamic},
		{name: "empty allocation mode", pools: []agentPool{{nodeSubnet, podSubnet, &empty}}, want: wantDynamic},
		{name: "equivalent legacy dynamic modes", pools: []agentPool{{nodeSubnet, podSubnet, nil}, {nodeSubnet, podSubnet, &empty}, dynamicPool}, want: wantDynamic},
		{name: "equivalent static pools", pools: []agentPool{staticPool, staticPool}, want: wantStatic},
		{
			name:  "case equivalent dynamic pools preserve selected tuple",
			pools: []agentPool{{strings.ToUpper(nodeSubnet), strings.ToUpper(podSubnet), &lowerDynamic}, dynamicPool},
			want:  networkSettings{strings.ToUpper(nodeSubnet), strings.ToUpper(podSubnet), lowerDynamic, "fake-vnet-guid"},
		},
		{
			name:  "case equivalent static pools preserve selected tuple",
			pools: []agentPool{{strings.ToUpper(nodeSubnet), strings.ToUpper(podSubnet), &lowerStatic}, staticPool},
			want:  networkSettings{strings.ToUpper(nodeSubnet), strings.ToUpper(podSubnet), lowerStatic, "fake-vnet-guid"},
		},
		{name: "no pod subnet", pools: []agentPool{noPodPool}, want: wantNoPod},
		{name: "heterogeneous node subnets without pod subnets", pools: []agentPool{noPodPool, {VnetSubnetID: nodeSubnet + "-other"}}, want: wantNoPod},
		{name: "managed vnet", pools: []agentPool{{}}, want: wantNoPod},
	}
	for _, mode := range []string{"bootstrappingclient", "aksmachineapi", "aksmachineapiheaderbatch"} {
		tests = append(tests,
			testCase{name: mode + " with pod subnet", mode: mode, pools: []agentPool{dynamicPool}, wantErr: "unsupported with provision-mode '" + mode + "'"},
			testCase{name: mode + " with pod-free first pool", mode: mode, pools: []agentPool{noPodPool, staticPool}, wantErr: "unsupported with provision-mode '" + mode + "'"},
			testCase{name: mode + " without pod subnet", mode: mode, pools: []agentPool{noPodPool}, want: wantNoPod},
		)
	}
	for _, mismatch := range []struct {
		name string
		pool agentPool
	}{
		{"node subnet", agentPool{nodeSubnet + "-other", podSubnet, &dynamic}},
		{"pod subnet", agentPool{nodeSubnet, podSubnet + "-other", &dynamic}},
		{"allocation mode", staticPool},
		{"pod-free pool", noPodPool},
	} {
		tests = append(tests,
			testCase{name: "different " + mismatch.name, pools: []agentPool{dynamicPool, mismatch.pool}, wantErr: ambiguous},
			testCase{name: "different " + mismatch.name + " reversed", pools: []agentPool{mismatch.pool, dynamicPool}, wantErr: ambiguous},
		)
	}

	script, err := filepath.Abs("configure-values.sh")
	if err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile("../../karpenter-values-template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"jq", "yq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("configure-values.sh tests require %s: %v", tool, err)
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile := func(name string, content []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), content, mode); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(dir, ".ssh"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(".ssh/id_rsa.pub", []byte("fake-public-key"), 0o600)
			writeFile("karpenter-values-template.yaml", template, 0o600)
			aks, err := json.Marshal(map[string]any{
				"agentPoolProfiles": tt.pools,
				"location":          "test-location",
				"nodeResourceGroup": "node-rg",
				"networkProfile":    map[string]string{"networkPlugin": "azure"},
				"identityProfile": map[string]any{
					"kubeletidentity": map[string]string{"resourceId": "fake-identity", "clientId": "fake-client"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			writeFile("aks.json", aks, 0o600)
			writeFile("vnet.json", []byte(`{"resourceGuid":"fake-vnet-guid","subnets":[{"id":"`+nodeSubnet+`"}]}`), 0o600)
			writeFile("az", []byte(`#!/usr/bin/env bash
set -euo pipefail
printf 'az %s\n' "$*" >> "$COMMAND_LOG"
case "$*" in
    "aks show --name test-cluster --resource-group test-rg --output json") cat "$AKS_FIXTURE" ;;
    "account show --query id --output tsv") echo fake-subscription ;;
    "network vnet show --ids $EXPECTED_VNET_ID --output json") cat "$VNET_FIXTURE" ;;
    "network vnet list --resource-group node-rg --output json") jq -s . "$VNET_FIXTURE" ;;
    "identity show --resource-group test-rg --name fake-identity --query clientId --output tsv") echo fake-client ;;
    *) echo "Unexpected az invocation: $*" >&2; exit 1 ;;
esac
`), 0o700)
			writeFile("kubectl", []byte(`#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "$COMMAND_LOG"
case "$*" in
    "config view --minify "*) echo https://fake-cluster.invalid ;;
    "get -n kube-system secrets "*) echo fake-token ;;
    "get -n kube-system secret fake-token "*) echo ZmFrZQ== ;;
    *) echo "Unexpected kubectl invocation: $*" >&2; exit 1 ;;
esac
`), 0o700)
			writeFile("curl", []byte("#!/usr/bin/env bash\necho 'Unexpected network access' >&2\nexit 1\n"), 0o700)

			args := []string{script, "test-cluster", "test-rg", "fake-service-account", "fake-identity", "false"}
			if tt.mode != "" {
				args = append(args, tt.mode)
			}
			cmd := exec.CommandContext(t.Context(), "bash", args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HOME="+dir,
				"AKS_FIXTURE="+filepath.Join(dir, "aks.json"),
				"VNET_FIXTURE="+filepath.Join(dir, "vnet.json"),
				"COMMAND_LOG="+filepath.Join(dir, "commands.log"),
				"EXPECTED_VNET_ID="+path.Dir(path.Dir(tt.want.VnetSubnetID)),
			)
			output, runErr := cmd.CombinedOutput()
			valuesPath := filepath.Join(dir, "karpenter-values.yaml")
			if tt.wantErr != "" {
				if runErr == nil {
					t.Fatalf("expected failure, got success:\n%s", output)
				}
				if !strings.Contains(string(output), tt.wantErr) {
					t.Fatalf("expected error %q, got %v:\n%s", tt.wantErr, runErr, output)
				}
				if tt.wantErr == ambiguous {
					for _, setting := range []string{"Helm/controller", "manually", "VNET_SUBNET_ID", "POD_SUBNET_ID", "POD_IP_ALLOCATION_MODE"} {
						if !strings.Contains(string(output), setting) {
							t.Errorf("missing manual configuration guidance %q:\n%s", setting, output)
						}
					}
				} else if !strings.Contains(string(output), "aksscriptless") {
					t.Errorf("missing recovery guidance to use aksscriptless:\n%s", output)
				}
				if _, err := os.Stat(valuesPath); !os.IsNotExist(err) {
					t.Fatalf("rejected configuration must not generate values: %v", err)
				}
				calls, err := os.ReadFile(filepath.Join(dir, "commands.log"))
				if err != nil {
					t.Fatal(err)
				}
				if string(calls) != "az aks show --name test-cluster --resource-group test-rg --output json\n" {
					t.Fatalf("validation must precede secret reads and other infrastructure calls:\n%s", calls)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("configure-values.sh failed: %v\n%s", runErr, output)
			}
			values, err := os.ReadFile(valuesPath)
			if err != nil {
				t.Fatal(err)
			}
			var generated struct {
				Settings struct {
					PodSubnetID         string `json:"podSubnetID"`
					PodIPAllocationMode string `json:"podIPAllocationMode"`
				} `json:"settings"`
				Controller struct {
					Env []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"controller"`
			}
			if err := yaml.Unmarshal(values, &generated); err != nil {
				t.Fatalf("invalid generated values: %v\n%s", err, values)
			}
			got := networkSettings{
				PodSubnetID:         generated.Settings.PodSubnetID,
				PodIPAllocationMode: generated.Settings.PodIPAllocationMode,
			}
			for _, variable := range generated.Controller.Env {
				switch variable.Name {
				case "VNET_SUBNET_ID":
					got.VnetSubnetID = variable.Value
				case "VNET_GUID":
					got.VnetGUID = variable.Value
				case "POD_SUBNET_ID", "POD_IP_ALLOCATION_MODE":
					t.Fatalf("%s in controller.env would override settings.*", variable.Name)
				}
			}
			if got != tt.want {
				t.Errorf("network settings = %+v, want %+v", got, tt.want)
			}
		})
	}
}
