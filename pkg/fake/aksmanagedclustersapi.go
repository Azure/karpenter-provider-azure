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
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
)

type AKSManagedClustersAPI struct {
	DefaultSupportedVersions  []string
	OverrideSupportedVersions []string
	Error                     error
}

var _ azapi.AKSManagedClustersAPI = &AKSManagedClustersAPI{}

func NewAKSManagedClustersAPI(defaultSupportedVersions ...string) *AKSManagedClustersAPI {
	return &AKSManagedClustersAPI{DefaultSupportedVersions: defaultSupportedVersions}
}

func (api *AKSManagedClustersAPI) ListKubernetesVersions(
	_ context.Context,
	_ string,
	_ *armcontainerservice.ManagedClustersClientListKubernetesVersionsOptions,
) (armcontainerservice.ManagedClustersClientListKubernetesVersionsResponse, error) {
	if api.Error != nil {
		return armcontainerservice.ManagedClustersClientListKubernetesVersionsResponse{}, api.Error
	}

	versions := api.DefaultSupportedVersions
	if api.OverrideSupportedVersions != nil {
		versions = api.OverrideSupportedVersions
	}
	patchVersions := map[string]*armcontainerservice.KubernetesPatchVersion{}
	for _, version := range versions {
		patchVersions[version] = &armcontainerservice.KubernetesPatchVersion{}
	}

	return armcontainerservice.ManagedClustersClientListKubernetesVersionsResponse{
		KubernetesVersionListResult: armcontainerservice.KubernetesVersionListResult{
			Values: []*armcontainerservice.KubernetesVersion{{PatchVersions: patchVersions}},
		},
	}, nil
}

func (api *AKSManagedClustersAPI) Reset() {
	api.OverrideSupportedVersions = nil
	api.Error = nil
}
