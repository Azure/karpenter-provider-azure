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

// realisticMachineProps returns a fully-populated MachineProperties matching
// production templates built by buildAKSMachineTemplate. All nested structs
// are populated so tests can verify that a single leaf-value change is enough
// to produce a different batch key.
func realisticMachineProps(vmSize, nodeClaimName string) *armcontainerservice.MachineProperties {
	return &armcontainerservice.MachineProperties{
		Hardware: &armcontainerservice.MachineHardwareProfile{
			VMSize: lo.ToPtr(vmSize),
			GpuProfile: &armcontainerservice.GPUProfile{
				Driver: lo.ToPtr(armcontainerservice.GPUDriverInstall),
			},
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
			ArtifactStreamingProfile: &armcontainerservice.AgentPoolArtifactStreamingProfile{
				Enabled: lo.ToPtr(true),
			},
		},
		OperatingSystem: &armcontainerservice.MachineOSProfile{
			OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
			OSSKU:        lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
			OSDiskSizeGB: lo.ToPtr[int32](128),
			OSDiskType:   lo.ToPtr(armcontainerservice.OSDiskTypeEphemeral),
			EnableFIPS:   lo.ToPtr(false),
			LinuxProfile: &armcontainerservice.MachineOSProfileLinuxProfile{
				LinuxOSConfig: &armcontainerservice.LinuxOSConfig{
					Sysctls: &armcontainerservice.SysctlConfig{
						NetCoreRmemMax: lo.ToPtr[int32](16777216),
						NetCoreSomaxconn: lo.ToPtr[int32](4096),
					},
				},
			},
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
		LocalDNSProfile: &armcontainerservice.LocalDNSProfile{
			Mode: lo.ToPtr(armcontainerservice.LocalDNSModeRequired),
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

	// Simulate a realistic batch: 10 machines with the same shared template
	// but ALL per-machine and read-only fields varying simultaneously,
	// exactly as production would produce them.
	items := make([]aksMachineCreatePayload, 10)
	zones := []string{"1", "2", "3"}
	for i := range items {
		props := realisticMachineProps("Standard_D4s_v3", fmt.Sprintf("nodeclaim-%d", i))
		// Vary read-only fields (normally not set by Karpenter, but must not affect hash if they are)
		props.ETag = lo.ToPtr(fmt.Sprintf("etag-%d", i))
		props.ProvisioningState = lo.ToPtr("Creating")
		props.ResourceID = lo.ToPtr(fmt.Sprintf("/subscriptions/sub/resourceGroups/rg/machines/machine-%d", i))
		props.Status = &armcontainerservice.MachineStatus{}

		items[i] = aksMachineCreatePayload{
			machineName:       fmt.Sprintf("machine-%d", i),
			resourceGroupName: "rg",
			resourceName:      "cluster",
			agentPoolName:     "aksmachinepool",
			machineBody: &armcontainerservice.Machine{
				Name:       lo.ToPtr(fmt.Sprintf("machine-%d", i)),
				Zones:      []*string{lo.ToPtr(zones[i%len(zones)])},
				Properties: props,
			},
		}
	}

	baseHash := mustDetermineBatchKey(t, &items[0])
	g.Expect(baseHash).ToNot(gomega.BeEmpty())
	for i := 1; i < len(items); i++ {
		g.Expect(mustDetermineBatchKey(t, &items[i])).To(gomega.Equal(baseHash),
			"machine %d should hash the same as machine 0 despite different per-machine+read-only fields", i)
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
		// Hardware
		{"different VM size", func(p *armcontainerservice.MachineProperties) { p.Hardware.VMSize = lo.ToPtr("Standard_D8s_v3") }},
		{"GPU driver change (leaf in nested struct)", func(p *armcontainerservice.MachineProperties) {
			p.Hardware.GpuProfile.Driver = lo.ToPtr(armcontainerservice.GPUDriverNone)
		}},
		{"GPU profile removed", func(p *armcontainerservice.MachineProperties) { p.Hardware.GpuProfile = nil }},

		// Priority / Mode
		{"spot priority", func(p *armcontainerservice.MachineProperties) {
			p.Priority = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
		}},
		{"system mode", func(p *armcontainerservice.MachineProperties) {
			p.Mode = lo.ToPtr(armcontainerservice.AgentPoolModeSystem)
		}},

		// OperatingSystem (flat fields)
		{"different OS SKU", func(p *armcontainerservice.MachineProperties) {
			p.OperatingSystem.OSSKU = lo.ToPtr(armcontainerservice.OSSKUAzureLinux)
		}},
		{"FIPS enabled", func(p *armcontainerservice.MachineProperties) { p.OperatingSystem.EnableFIPS = lo.ToPtr(true) }},
		{"different OSDiskSizeGB", func(p *armcontainerservice.MachineProperties) { p.OperatingSystem.OSDiskSizeGB = lo.ToPtr[int32](256) }},
		{"different OSDiskType", func(p *armcontainerservice.MachineProperties) {
			p.OperatingSystem.OSDiskType = lo.ToPtr(armcontainerservice.OSDiskTypeManaged)
		}},

		// OperatingSystem (nested leaf: single sysctl value change)
		{"sysctl leaf value change (NetCoreRmemMax)", func(p *armcontainerservice.MachineProperties) {
			p.OperatingSystem.LinuxProfile.LinuxOSConfig.Sysctls.NetCoreRmemMax = lo.ToPtr[int32](8388608)
		}},
		{"sysctl leaf value change (NetCoreSomaxconn)", func(p *armcontainerservice.MachineProperties) {
			p.OperatingSystem.LinuxProfile.LinuxOSConfig.Sysctls.NetCoreSomaxconn = lo.ToPtr[int32](8192)
		}},
		{"LinuxOSConfig removed", func(p *armcontainerservice.MachineProperties) { p.OperatingSystem.LinuxProfile = nil }},

		// Kubernetes (flat fields)
		{"different K8s version", func(p *armcontainerservice.MachineProperties) { p.Kubernetes.OrchestratorVersion = lo.ToPtr("1.32.0") }},
		{"different MaxPods", func(p *armcontainerservice.MachineProperties) { p.Kubernetes.MaxPods = lo.ToPtr[int32](110) }},

		// Kubernetes (nested leaf: KubeletConfig value change)
		{"KubeletConfig CPUManagerPolicy change (leaf)", func(p *armcontainerservice.MachineProperties) {
			p.Kubernetes.KubeletConfig.CPUManagerPolicy = lo.ToPtr("none")
		}},
		{"KubeletConfig removed", func(p *armcontainerservice.MachineProperties) { p.Kubernetes.KubeletConfig = nil }},

		// Kubernetes (nested leaf: ArtifactStreaming bool flip)
		{"ArtifactStreaming disabled (leaf bool flip)", func(p *armcontainerservice.MachineProperties) {
			p.Kubernetes.ArtifactStreamingProfile.Enabled = lo.ToPtr(false)
		}},
		{"ArtifactStreaming removed", func(p *armcontainerservice.MachineProperties) {
			p.Kubernetes.ArtifactStreamingProfile = nil
		}},

		// Network
		{"different subnet", func(p *armcontainerservice.MachineProperties) { p.Network.VnetSubnetID = lo.ToPtr("/other/subnet") }},

		// NodeImageVersion
		{"different node image", func(p *armcontainerservice.MachineProperties) {
			p.NodeImageVersion = lo.ToPtr("AKSUbuntu-2404gen2containerd-2025.03.01")
		}},

		// Security (nested leaf: bool flip)
		{"EncryptionAtHost disabled (leaf bool flip)", func(p *armcontainerservice.MachineProperties) {
			p.Security.EnableEncryptionAtHost = lo.ToPtr(false)
		}},

		// LocalDNS (nested leaf: mode change, not nil→populated)
		{"LocalDNS mode change (leaf)", func(p *armcontainerservice.MachineProperties) {
			p.LocalDNSProfile.Mode = lo.ToPtr(armcontainerservice.LocalDNSModeDisabled)
		}},
		{"LocalDNS removed", func(p *armcontainerservice.MachineProperties) { p.LocalDNSProfile = nil }},
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

// TestMachineKeyFunc_PerMachineFieldsDoNotSplit verifies that per-machine fields
// (Tags, Zones, Machine Name, read-only fields) do NOT affect the batch key.
// This is the positive counterpart to TestMachineKeyFunc_RealisticMachinesDifferentConfigsSplit.
func TestMachineKeyFunc_PerMachineFieldsDoNotSplit(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	baseItem := aksMachineCreatePayload{
		machineName: "machine-base",
		machineBody: &armcontainerservice.Machine{
			Name:       lo.ToPtr("machine-base"),
			Zones:      []*string{lo.ToPtr("1")},
			Properties: realisticMachineProps("Standard_D4s_v3", "nc-base"),
		},
	}
	baseHash := mustDetermineBatchKey(t, &baseItem)

	tests := []struct {
		name   string
		modify func(m *armcontainerservice.Machine, p *aksMachineCreatePayload)
	}{
		// Per-machine: Tags
		{"different Tags", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.Tags = map[string]*string{
				"completely": lo.ToPtr("different-tags"),
			}
		}},
		{"nil Tags", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.Tags = nil
		}},
		{"extra Tags", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.Tags["extra-key"] = lo.ToPtr("extra-val")
		}},

		// Per-machine: Zones
		{"different zone", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Zones = []*string{lo.ToPtr("3")}
		}},
		{"multiple zones", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Zones = []*string{lo.ToPtr("1"), lo.ToPtr("2"), lo.ToPtr("3")}
		}},
		{"nil zones", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Zones = nil
		}},

		// Per-machine: Machine Name (on payload and Machine object)
		{"different machine name", func(m *armcontainerservice.Machine, p *aksMachineCreatePayload) {
			m.Name = lo.ToPtr("different-machine")
			p.machineName = "different-machine"
		}},

		// Read-only fields
		{"ETag set", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.ETag = lo.ToPtr("etag-123")
		}},
		{"ProvisioningState set", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.ProvisioningState = lo.ToPtr("Succeeded")
		}},
		{"ResourceID set", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.ResourceID = lo.ToPtr("/subscriptions/sub/resourceGroups/rg/...")
		}},
		{"Status set", func(m *armcontainerservice.Machine, _ *aksMachineCreatePayload) {
			m.Properties.Status = &armcontainerservice.MachineStatus{}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			props := realisticMachineProps("Standard_D4s_v3", "nc-variant")
			machine := &armcontainerservice.Machine{
				Name:       lo.ToPtr("machine-variant"),
				Zones:      []*string{lo.ToPtr("1")},
				Properties: props,
			}
			item := aksMachineCreatePayload{
				machineName: "machine-variant",
				machineBody: machine,
			}
			tt.modify(machine, &item)
			g.Expect(mustDetermineBatchKey(t, &item)).To(gomega.Equal(baseHash),
				"per-machine field change (%s) should NOT affect batch key", tt.name)
		})
	}
}
