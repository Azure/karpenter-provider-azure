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

package inplaceupdate

import (
	"encoding/json"
	"hash/fnv"
	"strconv"

	"github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
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

// HashFromNodeClaim calculates an inplace update hash from the specified machine and options
func HashFromNodeClaim(options *options.Options, _ *v1beta1.NodeClaim) (string, error) {
	hashStruct := &inPlaceUpdateFields{
		Identities: sets.New(options.NodeIdentities...),
	}

	return hashStruct.CalculateHash()
}
