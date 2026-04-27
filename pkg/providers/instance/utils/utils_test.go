package utils

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
)

func TestHandleProvisioningState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name                    string
		provisioningState       string
		expectProvisioningError bool
		expectPollerError       bool
		expectDone              bool
	}{
		{
			name:                    "creating state",
			provisioningState:       "Creating",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "updating state",
			provisioningState:       "Updating",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "deleting state",
			provisioningState:       "Deleting",
			expectProvisioningError: false,
			expectPollerError:       true,
			expectDone:              true,
		},
		{
			name:                    "succeeded state",
			provisioningState:       "Succeeded",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              true,
		},
		{
			name:                    "failed state with provisioning error",
			provisioningState:       "Failed",
			expectProvisioningError: true,
			expectPollerError:       false,
			expectDone:              true,
		},
		{
			name:                    "unrecognized state continues polling",
			provisioningState:       "UnknownState",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &MachineCache{}

			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: to.Ptr(tt.provisioningState),
				},
			}

			if tt.provisioningState == "Failed" {
				machine.Properties.Status = &armcontainerservice.MachineStatus{
					ProvisioningError: &armcontainerservice.ErrorDetail{
						Code:    to.Ptr("TestError"),
						Message: to.Ptr("Test error message"),
					},
				}
			}

			provisioningErr, pollerErr, done := c.handleProvisioningState(ctx, machine, "test-machine")

			if (provisioningErr != nil) != tt.expectProvisioningError {
				t.Errorf("handleProvisioningState() provisioningErr = %v, expectProvisioningError %v", provisioningErr, tt.expectProvisioningError)
			}
			if (pollerErr != nil) != tt.expectPollerError {
				t.Errorf("handleProvisioningState() pollerErr = %v, expectPollerError %v", pollerErr, tt.expectPollerError)
			}
			if done != tt.expectDone {
				t.Errorf("handleProvisioningState() done = %v, expectDone %v", done, tt.expectDone)
			}
		})
	}
}
