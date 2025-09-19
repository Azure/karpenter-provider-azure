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

package customscriptsbootstrap

import (
	"encoding/base64"
	"math"
	"strings"

	"github.com/samber/lo"

	"k8s.io/apimachinery/pkg/api/resource"
)

func hydrateBootstrapTokenIfNeeded(customDataDehydratable string, cseDehydratable string, bootstrapToken string) (string, string, error) {
	cseHydrated := strings.ReplaceAll(cseDehydratable, "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken)

	decodedCustomDataDehydratableInBytes, err := base64.StdEncoding.DecodeString(customDataDehydratable)
	if err != nil {
		return "", "", err
	}
	decodedCustomDataHydrated := strings.ReplaceAll(string(decodedCustomDataDehydratableInBytes), "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken)
	customDataHydrated := base64.StdEncoding.EncodeToString([]byte(decodedCustomDataHydrated))

	return customDataHydrated, cseHydrated, nil
}

func reverseVMMemoryOverhead(vmMemoryOverheadPercent float64, adjustedMemory float64) float64 {
	// This is not the best way to do it... But will be refactored later, given that retrieving the original memory properly might involves some restructure.
	// Due to the fact that it is abstracted behind the cloudprovider interface.
	return adjustedMemory / (1 - vmMemoryOverheadPercent)
}

func ConvertContainerLogMaxSizeToMB(containerLogMaxSize string) *int32 {
	q, err := resource.ParseQuantity(containerLogMaxSize)
	if err == nil {
		// This could be improved later
		return lo.ToPtr(int32(math.Round(q.AsApproximateFloat64() / 1024 / 1024)))
	}
	return nil
}

func ConvertPodMaxPids(podPidsLimit *int64) *int32 {
	if podPidsLimit != nil {
		podPidsLimitInt64 := *podPidsLimit
		if podPidsLimitInt64 > int64(math.MaxInt32) {
			// This could be improved later
			return lo.ToPtr(int32(math.MaxInt32))
		} else if podPidsLimitInt64 < 0 {
			// This as well
			return lo.ToPtr(int32(-1))
		} else {
			return lo.ToPtr(int32(podPidsLimitInt64)) // golint:ignore G115 already check overflow
		}
	}
	return nil
}
