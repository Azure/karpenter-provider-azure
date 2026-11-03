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
	"encoding/json"
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

// Header-specific types for the BatchPutMachine HTTP header.

// batchPutMachineHeader is the JSON structure sent via HTTP header to Azure.
type batchPutMachineHeader struct {
	BatchMachines []MachineEntry `json:"batchMachines"`
}

type MachineEntry struct {
	MachineName string            `json:"machineName"`
	Zones       []string          `json:"zones"`
	Tags        map[string]string `json:"tags"`
}

// buildBatchHeader creates the JSON for the BatchPutMachine HTTP header
func buildBatchHeader(batch *batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]) (string, []MachineEntry, error) {
	entries := make([]MachineEntry, 0, len(batch.Requests))
	for _, req := range batch.Requests {
		var tags map[string]string
		if req.Payload.machineBody.Properties != nil {
			tags = extractTags(req.Payload.machineBody.Properties.Tags)
		} else {
			tags = make(map[string]string)
		}
		entries = append(entries, MachineEntry{
			MachineName: req.Payload.machineName,
			Zones:       extractZones(req.Payload.machineBody.Zones),
			Tags:        tags,
		})
	}

	header := batchPutMachineHeader{
		BatchMachines: entries,
	}

	jsonBytes, err := json.Marshal(header)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal batch header: %w", err)
	}
	return string(jsonBytes), entries, nil
}

// Helpers to convert Azure SDK pointer types to concrete values.

func extractZones(zones []*string) []string {
	if len(zones) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(zones))
	for _, z := range zones {
		if z != nil {
			result = append(result, *z)
		}
	}
	return result
}

func extractTags(tags map[string]*string) map[string]string {
	if tags == nil {
		return make(map[string]string)
	}
	result := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			result[k] = *v
		}
	}
	return result
}
