// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package inplaceupdate

import (
	"encoding/json"
	"hash/fnv"
	"strconv"

	"github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter/pkg/apis/settings"
)

// According to https://pkg.go.dev/encoding/json#Marshal, it's safe to use map-types (and encoding/json in general) to produce
// strings deterministically.
type inPlaceUpdateFields struct {
	Identities sets.Set[string] `json:"identities,omitempty"`
}

func (i *inPlaceUpdateFields) CalculateHash() (string, error) {
	encoded, err := json.Marshal(i)
	if err != nil {
		return "", err
	}

	h := fnv.New32a()
	_, err = h.Write(encoded)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(h.Sum32()), 10), nil
}

func HashFromVM(vm *armcompute.VirtualMachine) (string, error) {
	identities := sets.Set[string]{}
	if vm.Identity != nil {
		for ident := range vm.Identity.UserAssignedIdentities {
			identities.Insert(ident)
		}
	}

	hashStruct := &inPlaceUpdateFields{
		Identities: identities,
	}

	return hashStruct.CalculateHash()
}

// HashFromNodeClaim calculates an inplace update hash from the specified machine and settings
func HashFromNodeClaim(settings *settings.Settings, _ *v1beta1.NodeClaim) (string, error) {
	hashStruct := &inPlaceUpdateFields{
		Identities: sets.New(settings.NodeIdentities...),
	}

	return hashStruct.CalculateHash()
}
