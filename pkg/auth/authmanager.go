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

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/jongio/azidext/go/azidext"
)

const (
	// auth methods
	AuthMethodSysMSI           = "system-assigned-msi"
	AuthMethodWorkloadIdentity = "workload-identity"
)

// AuthManager manages the authentication logic for Azure clients used by Karpenter to make requests
type AuthManager struct {
	authMethod string
	location   string
}

func NewAuthManagerWorkloadIdentity(location string) *AuthManager {
	return &AuthManager{
		authMethod: AuthMethodWorkloadIdentity,
		location:   location,
	}

}

func NewAuthManagerSystemAssignedMSI(location string) *AuthManager {
	return &AuthManager{
		authMethod: AuthMethodSysMSI,
		location:   location,
	}
}

// NewCredential provides a token credential
func (am AuthManager) NewCredential() (azcore.TokenCredential, error) {
	if am.authMethod == AuthMethodWorkloadIdentity {
		klog.V(2).Infoln("cred: using workload identity for new credential")
		return azidentity.NewDefaultAzureCredential(nil)
	}

	if am.authMethod == AuthMethodSysMSI {
		klog.V(2).Infoln("cred: using system assigned MSI for new credential")
		msiCred, err := azidentity.NewManagedIdentityCredential(nil)
		if err != nil {
			return nil, err
		}
		return msiCred, nil
	}

	return nil, fmt.Errorf("cred: unsupported auth method: %s", am.authMethod)
}

func (am AuthManager) NewAutorestAuthorizer() (autorest.Authorizer, error) {
	// TODO (charliedmcb): need to get track 2 support for the skewer API, and align all auth under workload identity in the same way within cred.go
	if am.authMethod == AuthMethodWorkloadIdentity {
		klog.V(2).Infoln("auth: using workload identity for new authorizer")
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("default cred: %w", err)
		}
		return azidext.NewTokenCredentialAdapter(cred, []string{azidext.DefaultManagementScope}), nil
	}

	if am.authMethod == AuthMethodSysMSI {
		klog.V(2).Infoln("auth: using system assigned MSI to retrieve access token")
		msiEndpoint, err := adal.GetMSIVMEndpoint()
		if err != nil {
			return nil, fmt.Errorf("getting the managed service identity endpoint: %w", err)
		}

		azureEnvironment, err := azure.EnvironmentFromName(am.location)
		if err != nil {
			return nil, fmt.Errorf("failed to get AzureEnvironment: %w", err)
		}

		token, err := adal.NewServicePrincipalTokenFromMSI(
			msiEndpoint,
			azureEnvironment.ServiceManagementEndpoint)
		if err != nil {
			return nil, fmt.Errorf("retrieve service principal token: %w", err)
		}
		return autorest.NewBearerAuthorizer(token), nil
	}

	return nil, fmt.Errorf("auth: unsupported auth method %s", am.authMethod)
}
