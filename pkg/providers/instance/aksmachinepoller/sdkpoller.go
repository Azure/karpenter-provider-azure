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

package aksmachinepoller

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
)

type SDKPollerAdapter struct {
	poller            *runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse]
	client            AKSMachineGetter
	resourceGroupName string
	clusterName       string
	agentPoolName     string
	aksMachineName    string
}

// Compile-time assertion that SDKPollerAdapter implements CreatePoller
var _ CreatePoller = (*SDKPollerAdapter)(nil)

func NewSDKPollerAdapter(
	poller *runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse],
	client AKSMachineGetter,
	resourceGroupName string,
	clusterName string,
	agentPoolName string,
	aksMachineName string,
) *SDKPollerAdapter {
	return &SDKPollerAdapter{
		poller:            poller,
		client:            client,
		resourceGroupName: resourceGroupName,
		clusterName:       clusterName,
		agentPoolName:     agentPoolName,
		aksMachineName:    aksMachineName,
	}
}

func (a *SDKPollerAdapter) PollUntilDone(ctx context.Context) (*armcontainerservice.ErrorDetail, error) {
	_, err := a.poller.PollUntilDone(ctx, nil) // This may panic if it is deleted mid-way.
	if err != nil {
		// Get once after begin create to retrieve error details. This is because if the poller returns error, the sdk doesn't let us look at the real results.
		failedAKSMachine, _ := a.getMachine(ctx)
		if failedAKSMachine.Properties != nil && failedAKSMachine.Properties.Status != nil && failedAKSMachine.Properties.Status.ProvisioningError != nil {
			// Could be quota error; will be handled with custom logic below
			return failedAKSMachine.Properties.Status.ProvisioningError, nil
		}
		// Not provisioning error. Won't handle.
		return nil, fmt.Errorf("failed to create AKS machine %q during LRO, AKS API returned error: %w", a.aksMachineName, err)
	}
	return nil, nil
}

func (a *SDKPollerAdapter) getMachine(ctx context.Context) (*armcontainerservice.Machine, error) {
	resp, err := a.client.Get(ctx, a.resourceGroupName, a.clusterName, a.agentPoolName, a.aksMachineName, nil)
	if err != nil {
		return nil, err
	}
	return lo.ToPtr(resp.Machine), nil
}
