package byok_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func TestShouldSkipDiskEncryptionOverride(t *testing.T) {
	tests := []struct {
		name                string
		provisionMode       string
		inClusterController bool
		want                bool
	}{
		{name: "self-hosted Machine API", provisionMode: consts.ProvisionModeAKSMachineAPI, inClusterController: true, want: true},
		{name: "self-hosted Machine API header batch", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, inClusterController: true, want: true},
		{name: "NAP Machine API", provisionMode: consts.ProvisionModeAKSMachineAPI, want: false},
		{name: "NAP Machine API header batch", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, want: false},
		{name: "self-hosted scriptless", provisionMode: consts.ProvisionModeAKSScriptless, inClusterController: true, want: false},
		{name: "NAP bootstrapping client", provisionMode: consts.ProvisionModeBootstrappingClient, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldSkipDiskEncryptionOverride(test.provisionMode, test.inClusterController); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}
