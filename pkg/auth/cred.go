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

package auth

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// NewCredential provides a token credential for msi and service principal auth
func NewCredential(cfg *Config) (azcore.TokenCredential, error) {
	if cfg == nil {
		return nil, fmt.Errorf("failed to create credential, nil config provided")
	}

	if cfg.UseNewCredWorkflow {
		return azidentity.NewDefaultAzureCredential(nil)
	}

	if cfg.UseManagedIdentityExtension || cfg.AADClientID == "msi" {
		msiCred, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(cfg.UserAssignedIdentityID),
		})
		if err != nil {
			return nil, err
		}
		return msiCred, nil
	}
	// service principal case
	cred, err := azidentity.NewClientSecretCredential(cfg.TenantID, cfg.AADClientID, cfg.AADClientSecret, nil)
	if err != nil {
		return nil, err
	}
	return cred, nil
}
