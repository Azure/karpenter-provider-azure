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

	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/samber/lo"
)

// According to https://pkg.go.dev/encoding/json#Marshal, it's safe to use map-types (and encoding/json in general) to produce
// strings deterministically.
type aksMachineInPlaceUpdateFields struct {
	// VM identities are handled server-side for AKS machines. No need here.
	Tags map[string]string `json:"tags,omitempty"`
}

type vmInPlaceUpdateFields struct {
	Identities sets.Set[string]  `json:"identities,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// CalculateHash computes a hash for any JSON-marshalable struct
func CalculateHash(data any) (string, error) {
	encoded, err := json.Marshal(data)
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

// HashFromNodeClaim calculates an inplace update hash from the specified options, nodeClaim, and nodeClass
func HashFromNodeClaim(options *options.Options, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (string, error) {
	tags := launchtemplate.Tags(options, nodeClass, nodeClaim)
	tagsForHash := lo.MapValues(tags, func(v *string, _ string) string {
		return lo.FromPtr(v)
	})

	var hashStruct any
	if _, isAKSMachine := instance.GetAKSMachineNameFromNodeClaim(nodeClaim); isAKSMachine {
		// AKS machine-based node
		hashStruct = &aksMachineInPlaceUpdateFields{
			Tags: tagsForHash,
		}
	} else {
		// VM instance-based node
		hashStruct = &vmInPlaceUpdateFields{
			Identities: sets.New(options.NodeIdentities...),
			Tags:       tagsForHash,
		}
	}

	return CalculateHash(hashStruct)
}
