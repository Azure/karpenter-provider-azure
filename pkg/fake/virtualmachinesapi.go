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

package fake

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/samber/lo"
)

type VirtualMachineCreateOrUpdateInput struct {
	ResourceGroupName string
	VMName            string
	VM                armcompute.VirtualMachine
	Options           *armcompute.VirtualMachinesClientBeginCreateOrUpdateOptions
}

type VirtualMachineUpdateInput struct {
	ResourceGroupName string
	VMName            string
	Updates           armcompute.VirtualMachineUpdate
	Options           *armcompute.VirtualMachinesClientBeginUpdateOptions
}

type VirtualMachineDeleteInput struct {
	ResourceGroupName string
	VMName            string
	Options           *armcompute.VirtualMachinesClientBeginDeleteOptions
}

type VirtualMachineGetInput struct {
	ResourceGroupName string
	VMName            string
	Options           *armcompute.VirtualMachinesClientGetOptions
}

type VirtualMachinesBehavior struct {
	VirtualMachineCreateOrUpdateBehavior MockedLRO[VirtualMachineCreateOrUpdateInput, armcompute.VirtualMachinesClientCreateOrUpdateResponse]
	VirtualMachineUpdateBehavior         MockedLRO[VirtualMachineUpdateInput, armcompute.VirtualMachinesClientUpdateResponse]
	VirtualMachineDeleteBehavior         MockedLRO[VirtualMachineDeleteInput, armcompute.VirtualMachinesClientDeleteResponse]
	VirtualMachineGetBehavior            MockedFunction[VirtualMachineGetInput, armcompute.VirtualMachinesClientGetResponse]
	Instances                            sync.Map
}

// assert that the fake implements the interface
var _ instance.VirtualMachinesAPI = &VirtualMachinesAPI{}

type VirtualMachinesAPI struct {
	// TODO: document the implications of embedding vs. not embedding the interface here
	// instance.VirtualMachinesAPI // - this is the interface we are mocking.
	VirtualMachinesBehavior
	AuxiliaryTokenPolicy *auth.AuxiliaryTokenPolicy
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *VirtualMachinesAPI) Reset() {
	c.VirtualMachineCreateOrUpdateBehavior.Reset()
	c.VirtualMachineDeleteBehavior.Reset()
	c.VirtualMachineGetBehavior.Reset()
	c.VirtualMachineUpdateBehavior.Reset()
	c.Instances.Range(func(k, v any) bool {
		c.Instances.Delete(k)
		return true
	})
}

// UseAuxiliaryTokenPolicy simulates AuxiliaryTokenPolicy.Do() method being called at the beginning of each API call
// This is useful for testing scenarios where the auxiliary token is required for the API call to succeed.
// If the AuxiliaryTokenPolicy is not set (USE_SIG: false), this method does nothing and returns nil.
func (c *VirtualMachinesAPI) UseAuxiliaryTokenPolicy() error {
	if c.AuxiliaryTokenPolicy != nil {
		request, _ := runtime.NewRequest(context.Background(), "GET", "http://example.com")
		if _, err := c.AuxiliaryTokenPolicy.Do(request); err != nil {
			// req.Next() returns this if there are no more policies.
			if err.Error() == "no more policies" {
				return nil
			}
			return err
		}
	}
	return nil
}

func (c *VirtualMachinesAPI) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, vmName string, parameters armcompute.VirtualMachine, options *armcompute.VirtualMachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse], error) {
	// gather input parameters (may get rid of this with multiple mocked function signatures to reflect common patterns)
	input := &VirtualMachineCreateOrUpdateInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		VM:                parameters,
		Options:           options,
	}
	// BeginCreateOrUpdate should fail, if the vm exists in the cache, and we are attempting to change properties for zone

	return c.VirtualMachineCreateOrUpdateBehavior.Invoke(input, func(input *VirtualMachineCreateOrUpdateInput) (*armcompute.VirtualMachinesClientCreateOrUpdateResponse, error) {
		if err := c.UseAuxiliaryTokenPolicy(); err != nil {
			return nil, getAuthTokenError(err)
		}
		//if input.ResourceGroupName == "" {
		//	return nil, errors.New("ResourceGroupName is required")
		//}
		// TODO: may have to clone ...
		// TODO: subscription ID?
		vm := input.VM
		id := MkVMID(input.ResourceGroupName, input.VMName)
		vm.ID = lo.ToPtr(id)

		// Check store for existing vm by name
		existingVM, ok := c.Instances.Load(id)
		if ok {
			incomingZone := vm.Zones[0] // Note: this assumes at least 1 zone and only one zone is put on our vm
			existingZone := existingVM.(armcompute.VirtualMachine).Zones[0]
			if incomingZone != existingZone {
				// Currently only returning for zones, but osProfile.customData will also return this error
				errCode := "PropertyChangeNotAllowed"
				msg := `Creating virtual machine "aks-default-4984v" failed: PUT https://management.azure.com/subscriptions/****/resourceGroups/****/providers/Microsoft.Compute/virtualMachines/aks-default-4984v
--------------------------------------------------------------------------------
RESPONSE 409: 409 Conflict
ERROR CODE: PropertyChangeNotAllowed
--------------------------------------------------------------------------------
{
  "error": {
    "code": "PropertyChangeNotAllowed",
    "message": "Changing property 'zones' is not allowed.",
    "target": "zones"
  }
}
--------------------------------------------------------------------------------`
				return nil, &azcore.ResponseError{
					ErrorCode: errCode,
					RawResponse: &http.Response{
						Body: createSDKErrorBody(errCode, msg),
					},
				}
			}
			// Use existing vm rather than restoring
			return &armcompute.VirtualMachinesClientCreateOrUpdateResponse{VirtualMachine: existingVM.(armcompute.VirtualMachine)}, nil
		}

		vm.Name = lo.ToPtr(input.VMName)
		if vm.Properties == nil {
			vm.Properties = &armcompute.VirtualMachineProperties{}
		}
		if vm.Properties.TimeCreated == nil {
			vm.Properties.TimeCreated = lo.ToPtr(time.Now()) // TODO: use simulated time?
		}
		c.Instances.Store(id, vm)
		return &armcompute.VirtualMachinesClientCreateOrUpdateResponse{VirtualMachine: vm}, nil
	})
}

func (c *VirtualMachinesAPI) BeginUpdate(_ context.Context, resourceGroupName string, vmName string, updates armcompute.VirtualMachineUpdate, options *armcompute.VirtualMachinesClientBeginUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientUpdateResponse], error) {
	input := &VirtualMachineUpdateInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		Updates:           updates,
		Options:           options,
	}
	return c.VirtualMachineUpdateBehavior.Invoke(input, func(input *VirtualMachineUpdateInput) (*armcompute.VirtualMachinesClientUpdateResponse, error) {
		if err := c.UseAuxiliaryTokenPolicy(); err != nil {
			return nil, getAuthTokenError(err)
		}
		id := MkVMID(input.ResourceGroupName, input.VMName)

		instance, ok := c.Instances.Load(id)
		if !ok {
			return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
		}
		vm := instance.(armcompute.VirtualMachine)

		// If other fields need to be updated in the future, you can similarly
		// update the VM object by merging with updates.<New Field>.
		if updates.Tags != nil {
			// VM tags are full-replace if they're specified
			vm.Tags = maps.Clone(updates.Tags)
		}
		if updates.Identity != nil {
			if vm.Identity == nil {
				vm.Identity = &armcompute.VirtualMachineIdentity{}
			}

			if updates.Identity.Type != nil {
				vm.Identity.Type = updates.Identity.Type
			}
			if len(updates.Identity.UserAssignedIdentities) > 0 {
				if vm.Identity.UserAssignedIdentities == nil {
					vm.Identity.UserAssignedIdentities = make(map[string]*armcompute.UserAssignedIdentitiesValue)
				}
				for id, val := range updates.Identity.UserAssignedIdentities {
					vm.Identity.UserAssignedIdentities[id] = val
				}
			}
		}

		// Update the stored shape
		c.Instances.Store(id, vm)

		return &armcompute.VirtualMachinesClientUpdateResponse{VirtualMachine: vm}, nil
	})
}

func (c *VirtualMachinesAPI) Get(_ context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientGetOptions) (armcompute.VirtualMachinesClientGetResponse, error) {
	input := &VirtualMachineGetInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		Options:           options,
	}
	return c.VirtualMachineGetBehavior.Invoke(input, func(input *VirtualMachineGetInput) (armcompute.VirtualMachinesClientGetResponse, error) {
		if err := c.UseAuxiliaryTokenPolicy(); err != nil {
			return armcompute.VirtualMachinesClientGetResponse{}, getAuthTokenError(err)
		}
		instance, ok := c.Instances.Load(MkVMID(input.ResourceGroupName, input.VMName))
		if !ok {
			return armcompute.VirtualMachinesClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound}
		}
		return armcompute.VirtualMachinesClientGetResponse{
			VirtualMachine: instance.(armcompute.VirtualMachine),
		}, nil
	})
}

func (c *VirtualMachinesAPI) BeginDelete(_ context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientBeginDeleteOptions) (*runtime.Poller[armcompute.VirtualMachinesClientDeleteResponse], error) {
	input := &VirtualMachineDeleteInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		Options:           options,
	}
	return c.VirtualMachineDeleteBehavior.Invoke(input, func(input *VirtualMachineDeleteInput) (*armcompute.VirtualMachinesClientDeleteResponse, error) {
		if err := c.UseAuxiliaryTokenPolicy(); err != nil {
			return &armcompute.VirtualMachinesClientDeleteResponse{}, getAuthTokenError(err)
		}
		c.Instances.Delete(MkVMID(input.ResourceGroupName, input.VMName))
		return &armcompute.VirtualMachinesClientDeleteResponse{}, nil
	})
}

func createSDKErrorBody(code, message string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf(`{"error":{"code": "%s", "message": "%s"}}`, code, message))))
}

func MkVMID(resourceGroupName string, vmName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName)
}

func getAuthTokenError(err error) *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode: "AuthenticationFailed",
		RawResponse: &http.Response{
			Body: createSDKErrorBody("AuthenticationFailed", err.Error()),
		},
	}
}
