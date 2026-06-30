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

package aksmachinesheaderbatch

import (
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/onsi/gomega"
	"github.com/samber/lo"
)

func keyOf(vmSize string, zones []string, tags map[string]string) string {
	m := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}
	for _, z := range zones {
		m.Zones = append(m.Zones, &z)
	}
	if len(tags) > 0 {
		m.Properties.Tags = make(map[string]*string, len(tags))
		for k, v := range tags {
			m.Properties.Tags[k] = &v
		}
	}
	item := aksMachineCreatePayload{machineBody: m}
	key, _ := determineBatchKey(&item)
	return key
}

func mustDetermineBatchKey(t *testing.T, item *aksMachineCreatePayload) string {
	t.Helper()
	key, err := determineBatchKey(item)
	if err != nil {
		t.Fatalf("determineBatchKey failed: %v", err)
	}
	return key
}

func TestMachineKeyFunc(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	h1 := keyOf("Standard_D2s_v3", []string{"1"}, nil)
	h2 := keyOf("Standard_D2s_v3", []string{"2"}, nil)
	h3 := keyOf("Standard_D4s_v3", []string{"1"}, nil)

	g.Expect(h2).To(gomega.Equal(h1), "hashes should be equal when only zones differ")
	g.Expect(h3).ToNot(gomega.Equal(h1), "hashes should differ when VM size differs")
}

func TestMachineKeyFunc_TagsExcluded(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	h1 := keyOf("Standard_D2s_v3", nil, map[string]string{"nodeclaim": "nc-abc"})
	h2 := keyOf("Standard_D2s_v3", nil, nil)
	h3 := keyOf("Standard_D2s_v3", nil, map[string]string{"nodeclaim": "nc-xyz"})

	g.Expect(h2).To(gomega.Equal(h1), "tags should not affect hash")
	g.Expect(h3).To(gomega.Equal(h1), "different tags should not affect hash")
}

func TestMachineKeyFunc_ReadOnlyFieldsExcluded(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	vmSize := "Standard_D2s_v3"
	item1 := aksMachineCreatePayload{machineBody: &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware:          &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			ETag:              lo.ToPtr("etag-123"),
			ProvisioningState: lo.ToPtr("Succeeded"),
			ResourceID:        lo.ToPtr("/subscriptions/sub/resourceGroups/rg/..."),
		},
	}}
	item2 := aksMachineCreatePayload{machineBody: &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}}

	g.Expect(mustDetermineBatchKey(t, &item2)).To(gomega.Equal(mustDetermineBatchKey(t, &item1)), "read-only fields should not affect hash")
}

func TestMachineKeyFunc_UseWindowsGen2VMSeparatesBatches(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	vmSize := "Standard_D2s_v3"
	body := func() *armcontainerservice.Machine {
		return &armcontainerservice.Machine{Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		}}
	}
	gen1 := aksMachineCreatePayload{machineBody: body(), useWindowsGen2VM: false}
	gen2 := aksMachineCreatePayload{machineBody: body(), useWindowsGen2VM: true}

	g.Expect(mustDetermineBatchKey(t, &gen2)).ToNot(gomega.Equal(mustDetermineBatchKey(t, &gen1)),
		"machines requesting different Windows image generations must not share a batch")
}

// realisticMachineProps returns a fully-populated MachineProperties matching
// production templates built by buildAKSMachineTemplate.
func realisticMachineProps(vmSize, nodeClaimName string) *armcontainerservice.MachineProperties {
	return &armcontainerservice.MachineProperties{
		Hardware: &armcontainerservice.MachineHardwareProfile{
			VMSize: lo.ToPtr(vmSize),
		},
		Kubernetes: &armcontainerservice.MachineKubernetesProfile{
			OrchestratorVersion: lo.ToPtr("1.31.0"),
			NodeLabels: map[string]*string{
				"karpenter.sh_nodepool":            lo.ToPtr("default"),
				"karpenter.sh_capacity-type":       lo.ToPtr("on-demand"),
				"node.kubernetes.io_instance-type": lo.ToPtr(vmSize),
			},
			NodeInitializationTaints: []*string{
				lo.ToPtr("node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule"),
			},
			NodeTaints: []*string{},
			MaxPods:    lo.ToPtr[int32](250),
			KubeletConfig: &armcontainerservice.KubeletConfig{
				CPUManagerPolicy: lo.ToPtr("static"),
			},
		},
		OperatingSystem: &armcontainerservice.MachineOSProfile{
			OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
			OSSKU:        lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
			OSDiskSizeGB: lo.ToPtr[int32](128),
			OSDiskType:   lo.ToPtr(armcontainerservice.OSDiskTypeEphemeral),
			EnableFIPS:   lo.ToPtr(false),
		},
		Network: &armcontainerservice.MachineNetworkProperties{
			VnetSubnetID: lo.ToPtr("/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/nodesubnet"),
		},
		Priority:         lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
		Mode:             lo.ToPtr(armcontainerservice.AgentPoolModeUser),
		NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-2024.12.15"),
		Security: &armcontainerservice.MachineSecurityProfile{
			SSHAccess:              lo.ToPtr(armcontainerservice.AgentPoolSSHAccessLocalUser),
			EnableEncryptionAtHost: lo.ToPtr(true),
		},
		Tags: map[string]*string{
			"karpenter.azure.com_cluster":              lo.ToPtr("prod-cluster"),
			"karpenter.sh_nodepool":                    lo.ToPtr("default"),
			"karpenter.azure.com_aksmachine_nodeclaim": lo.ToPtr(nodeClaimName),
		},
	}
}

func TestMachineKeyFunc_RealisticMachinesBatchTogether(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	items := make([]aksMachineCreatePayload, 10)
	zones := []string{"1", "2", "3"}
	for i := range items {
		items[i] = aksMachineCreatePayload{
			machineName: fmt.Sprintf("machine-%d", i),
			machineBody: &armcontainerservice.Machine{
				Name:       lo.ToPtr(fmt.Sprintf("machine-%d", i)),
				Zones:      []*string{lo.ToPtr(zones[i%len(zones)])},
				Properties: realisticMachineProps("Standard_D4s_v3", fmt.Sprintf("nodeclaim-%d", i)),
			},
		}
	}

	baseHash := mustDetermineBatchKey(t, &items[0])
	g.Expect(baseHash).ToNot(gomega.BeEmpty())
	for i := 1; i < len(items); i++ {
		g.Expect(mustDetermineBatchKey(t, &items[i])).To(gomega.Equal(baseHash),
			"machine %d should hash the same as machine 0", i)
	}
}

func TestMachineKeyFunc_RealisticMachinesDifferentConfigsSplit(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	baseItem := aksMachineCreatePayload{
		machineBody: &armcontainerservice.Machine{
			Properties: realisticMachineProps("Standard_D4s_v3", "nc-0"),
		},
	}
	baseHash := mustDetermineBatchKey(t, &baseItem)

	tests := []struct {
		name   string
		modify func(p *armcontainerservice.MachineProperties)
	}{
		{"different VM size", func(p *armcontainerservice.MachineProperties) { p.Hardware.VMSize = lo.ToPtr("Standard_D8s_v3") }},
		{"spot priority", func(p *armcontainerservice.MachineProperties) {
			p.Priority = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
		}},
		{"different OS SKU", func(p *armcontainerservice.MachineProperties) {
			p.OperatingSystem.OSSKU = lo.ToPtr(armcontainerservice.OSSKUAzureLinux)
		}},
		{"different K8s version", func(p *armcontainerservice.MachineProperties) { p.Kubernetes.OrchestratorVersion = lo.ToPtr("1.32.0") }},
		{"different subnet", func(p *armcontainerservice.MachineProperties) { p.Network.VnetSubnetID = lo.ToPtr("/other/subnet") }},
		{"system mode", func(p *armcontainerservice.MachineProperties) {
			p.Mode = lo.ToPtr(armcontainerservice.AgentPoolModeSystem)
		}},
		{"FIPS enabled", func(p *armcontainerservice.MachineProperties) { p.OperatingSystem.EnableFIPS = lo.ToPtr(true) }},
		{"different node image", func(p *armcontainerservice.MachineProperties) {
			p.NodeImageVersion = lo.ToPtr("AKSUbuntu-2404gen2containerd-2025.03.01")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			props := realisticMachineProps("Standard_D4s_v3", "nc-0")
			tt.modify(props)
			item := aksMachineCreatePayload{machineBody: &armcontainerservice.Machine{Properties: props}}
			g.Expect(mustDetermineBatchKey(t, &item)).ToNot(gomega.Equal(baseHash), "hash should differ when %s changes", tt.name)
		})
	}
}
