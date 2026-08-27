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

package azure

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	replacementKubeletIdentityResourceIDEnv  = "AKS_E2E_REPLACEMENT_KUBELET_IDENTITY_RESOURCE_ID"
	replacementKubeletIdentityClientIDEnv    = "AKS_E2E_REPLACEMENT_KUBELET_IDENTITY_CLIENT_ID"
	replacementKubeletIdentityPrincipalIDEnv = "AKS_E2E_REPLACEMENT_KUBELET_IDENTITY_PRINCIPAL_ID"
)

func replacementKubeletIdentityFromEnvironment() (*armmsi.Identity, bool, error) {
	resourceIDValue, resourceIDSet := os.LookupEnv(replacementKubeletIdentityResourceIDEnv)
	clientIDValue, clientIDSet := os.LookupEnv(replacementKubeletIdentityClientIDEnv)
	principalIDValue, principalIDSet := os.LookupEnv(replacementKubeletIdentityPrincipalIDEnv)
	if !resourceIDSet && !clientIDSet && !principalIDSet {
		return nil, false, nil
	}
	resourceID := strings.TrimSpace(resourceIDValue)
	clientID := strings.TrimSpace(clientIDValue)
	principalID := strings.TrimSpace(principalIDValue)
	if !resourceIDSet || !clientIDSet || !principalIDSet || resourceID == "" || clientID == "" || principalID == "" {
		return nil, false, fmt.Errorf("%s, %s, and %s must all be configured with non-empty values", replacementKubeletIdentityResourceIDEnv, replacementKubeletIdentityClientIDEnv, replacementKubeletIdentityPrincipalIDEnv)
	}
	return &armmsi.Identity{
		ID: to.Ptr(resourceID),
		Properties: &armmsi.UserAssignedIdentityProperties{
			ClientID:    to.Ptr(clientID),
			PrincipalID: to.Ptr(principalID),
		},
	}, true, nil
}

// ExpectPreparedKubeletIdentity uses an outer-harness fixture when supplied.
// The NAP harness runs the state-mutating Machine suite once per cluster so the
// stable fixture cannot be reused as both the original and replacement identity.
// Self-hosted runs retain the existing in-test identity and AcrPull setup.
func (env *Environment) ExpectPreparedKubeletIdentity(ctx context.Context, identityName string) *armmsi.Identity {
	GinkgoHelper()
	identity, configured, err := replacementKubeletIdentityFromEnvironment()
	Expect(err).ToNot(HaveOccurred())
	if configured {
		By("using the replacement kubelet identity prepared by the outer E2E harness")
		return identity
	}
	identity = env.ExpectCreatedManagedIdentity(ctx, identityName)
	env.ExpectGrantedACRAccess(ctx, identity)
	return identity
}
