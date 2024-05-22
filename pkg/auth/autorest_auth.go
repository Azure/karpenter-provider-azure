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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/jongio/azidext/go/azidext"
)

func NewAuthorizer(config *Config, env *azure.Environment) (autorest.Authorizer, error) {
	// TODO (charliedmcb): need to get track 2 support for the skewer API, and align all auth under workload identity in the same way within cred.go
	if config.AuthMethod == authMethodCredFromEnv {
		klog.V(2).Infoln("auth: using workload identity for new authorizer")
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("default cred: %w", err)
		}
		return azidext.NewTokenCredentialAdapter(cred, []string{azidext.DefaultManagementScope}), nil
	}

	if config.AuthMethod == authMethodSysMSI {
		klog.V(2).Infoln("auth: using system assigned MSI to retrieve access token")
		msiEndpoint, err := adal.GetMSIVMEndpoint()
		if err != nil {
			return nil, fmt.Errorf("getting the managed service identity endpoint: %w", err)
		}

		token, err := adal.NewServicePrincipalTokenFromMSI(
			msiEndpoint,
			env.ServiceManagementEndpoint)
		if err != nil {
			return nil, fmt.Errorf("retrieve service principal token: %w", err)
		}
		return autorest.NewBearerAuthorizer(token), nil
	}

	return nil, fmt.Errorf("auth: unsupported auth method %s", config.AuthMethod)
}
