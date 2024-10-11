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
	"context"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type ErrorRetriever interface {
	GetFrontendErr() error
	WaitForAsyncErr(ctx context.Context) error
}

type errorRetriever struct {
	frontendErr    error
	asyncErrPoller func(ctx context.Context) error
}

// T is the type of the arm response object
func NewErrorRetriever[T any](frontendErr error, asyncPoller *runtime.Poller[T]) ErrorRetriever {
	return &errorRetriever{
		frontendErr: frontendErr,
		asyncErrPoller: func(ctx context.Context) error {
			if asyncPoller == nil {
				return nil
			}
			_, err := asyncPoller.PollUntilDone(ctx, nil)
			return err
		},
	}
}

func (er *errorRetriever) GetFrontendErr() error {
	return er.frontendErr
}

func (er *errorRetriever) WaitForAsyncErr(ctx context.Context) error {
	return er.asyncErrPoller(ctx)
}

func CreateVirtualMachine(ctx context.Context, client VirtualMachinesAPI, rg, vmName string, vm armcompute.VirtualMachine) (*armcompute.VirtualMachine, ErrorRetriever) {
	poller, err := client.BeginCreateOrUpdate(ctx, rg, vmName, vm, nil)
	if err != nil {
		return nil, NewErrorRetriever[armcompute.VirtualMachinesClientCreateOrUpdateResponse](err, poller)
	}
	vmget, err := client.Get(ctx, rg, vmName, nil)
	if err != nil {
		return nil, NewErrorRetriever[armcompute.VirtualMachinesClientCreateOrUpdateResponse](err, poller)
	}
	return &vmget.VirtualMachine, NewErrorRetriever[armcompute.VirtualMachinesClientCreateOrUpdateResponse](nil, poller)
}

func UpdateVirtualMachine(ctx context.Context, client VirtualMachinesAPI, rg, vmName string, updates armcompute.VirtualMachineUpdate) error {
	poller, err := client.BeginUpdate(ctx, rg, vmName, updates, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}

func deleteVirtualMachine(ctx context.Context, client VirtualMachinesAPI, rg, vmName string) error {
	poller, err := client.BeginDelete(ctx, rg, vmName, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	return nil
}

func createVirtualMachineExtension(ctx context.Context, client VirtualMachineExtensionsAPI, rg, vmName, extensionName string, vmExt armcompute.VirtualMachineExtension) (*armcompute.VirtualMachineExtension, ErrorRetriever) {
	poller, err := client.BeginCreateOrUpdate(ctx, rg, vmName, extensionName, vmExt, nil)
	if err != nil {
		return nil, NewErrorRetriever[armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse](err, poller)
	}
	getExt, err := client.Get(ctx, rg, vmName, extensionName, nil)
	if err != nil {
		return nil, NewErrorRetriever[armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse](err, poller)
	}
	return &getExt.VirtualMachineExtension, NewErrorRetriever[armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse](nil, poller)
}

func createNic(ctx context.Context, client NetworkInterfacesAPI, rg, nicName string, nic armnetwork.Interface) (*armnetwork.Interface, ErrorRetriever) {
	poller, err := client.BeginCreateOrUpdate(ctx, rg, nicName, nic, nil)
	if err != nil {
		return nil, NewErrorRetriever[armnetwork.InterfacesClientCreateOrUpdateResponse](err, poller)
	}
	getNic, err := client.Get(ctx, rg, nicName, nil)
	if err != nil {
		return nil, NewErrorRetriever[armnetwork.InterfacesClientCreateOrUpdateResponse](err, poller)
	}
	return &getNic.Interface, NewErrorRetriever[armnetwork.InterfacesClientCreateOrUpdateResponse](nil, poller)
}

func deleteNic(ctx context.Context, client NetworkInterfacesAPI, rg, nicName string) error {
	poller, err := client.BeginDelete(ctx, rg, nicName, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	return nil
}

func deleteNicIfExists(ctx context.Context, client NetworkInterfacesAPI, rg, nicName string) error {
	_, err := client.Get(ctx, rg, nicName, nil)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	return deleteNic(ctx, client, rg, nicName)
}

// deleteVirtualMachineIfExists checks if a virtual machine exists, and if it does, we delete it with a cascading delete
func deleteVirtualMachineIfExists(ctx context.Context, client VirtualMachinesAPI, rg, vmName string) error {
	_, err := client.Get(ctx, rg, vmName, nil)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	return deleteVirtualMachine(ctx, client, rg, vmName)
}
