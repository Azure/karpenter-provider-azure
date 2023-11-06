// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

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
