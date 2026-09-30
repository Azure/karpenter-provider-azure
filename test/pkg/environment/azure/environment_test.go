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

package azure

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	containerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9/fake"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func TestSkipIfNotWindowsCapable(t *testing.T) {
	RegisterFailHandler(Fail)
	type observation struct {
		name                   string
		report                 SpecReport
		managedClusterGets     int
		wantSkipReason         string
		wantManagedClusterGets int
	}
	var observations []observation
	caseCount := 0
	tests := []struct {
		name                   string
		networkDataplane       string
		hasWindowsProfile      bool
		wantSkipReason         string
		wantManagedClusterGets int
	}{
		{
			name:                   "Cilium skips even with a Windows profile",
			networkDataplane:       common.NetworkDataplaneCilium,
			hasWindowsProfile:      true,
			wantSkipReason:         "Windows node provisioning is not supported with the Cilium dataplane",
			wantManagedClusterGets: 0,
		},
		{
			name:                   "Azure dataplane with a Windows profile runs",
			networkDataplane:       common.NetworkDataplaneAzure,
			hasWindowsProfile:      true,
			wantManagedClusterGets: 1,
		},
		{
			name:                   "Azure dataplane without a Windows profile skips",
			networkDataplane:       common.NetworkDataplaneAzure,
			wantSkipReason:         "cluster has no windowsProfile; Windows nodes require a Windows-capable cluster",
			wantManagedClusterGets: 1,
		},
	}
	for _, mode := range []string{consts.ProvisionModeAKSMachineAPI, consts.ProvisionModeAKSMachineAPIHeaderBatch} {
		for _, poolName := range []string{"mpool", "aksmanagedap"} {
			for _, tt := range tests {
				name := mode + "/" + poolName + "/" + tt.name
				caseCount++
				It(name, func() {
					properties := &containerservice.ManagedClusterProperties{}
					if tt.hasWindowsProfile {
						properties.WindowsProfile = &containerservice.ManagedClusterWindowsProfile{}
					}
					managedClusterGets := 0
					server := &containerservicefake.ManagedClustersServer{
						Get: func(_ context.Context, _, _ string, _ *containerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[containerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
							managedClusterGets++
							resp.SetResponse(http.StatusOK, containerservice.ManagedClustersClientGetResponse{
								ManagedCluster: containerservice.ManagedCluster{Properties: properties},
							}, nil)
							return resp, errResp
						},
					}
					managedClusterClient, err := containerservice.NewManagedClustersClient("subscription", &azfake.TokenCredential{}, &arm.ClientOptions{
						ClientOptions: azcore.ClientOptions{Transport: containerservicefake.NewManagedClustersServerTransport(server)},
					})
					Expect(err).ToNot(HaveOccurred())
					env := &Environment{
						Environment: &common.Environment{
							Context:             context.Background(),
							InClusterController: poolName != "aksmanagedap",
							NetworkDataplane:    tt.networkDataplane,
						},
						ProvisionMode:        mode,
						MachineAgentPoolName: poolName,
						ClusterResourceGroup: "resource-group",
						ClusterName:          "cluster",
						managedClusterClient: managedClusterClient,
					}

					// Failed cleanup assertions do not fail an already-skipped Ginkgo spec.
					// Collect results here and assert with testing.T after RunSpecs instead.
					DeferCleanup(func() {
						observations = append(observations, observation{
							name:                   name,
							report:                 CurrentSpecReport(),
							managedClusterGets:     managedClusterGets,
							wantSkipReason:         tt.wantSkipReason,
							wantManagedClusterGets: tt.wantManagedClusterGets,
						})
					})
					env.SkipIfNotWindowsCapable()
				})
			}
		}
	}
	RunSpecs(t, "Windows capability")
	NewWithT(t).Expect(observations).To(HaveLen(caseCount))
	for _, observed := range observations {
		t.Run(observed.name, func(t *testing.T) {
			g := NewWithT(t)
			wantState := types.SpecStatePassed
			if observed.wantSkipReason != "" {
				wantState = types.SpecStateSkipped
			}
			g.Expect(observed.report.State).To(Equal(wantState))
			g.Expect(observed.report.Failure.Message).To(Equal(observed.wantSkipReason))
			g.Expect(observed.managedClusterGets).To(Equal(observed.wantManagedClusterGets))
		})
	}
}
