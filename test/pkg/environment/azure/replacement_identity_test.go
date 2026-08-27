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
	"os"
	"testing"
)

func clearReplacementKubeletIdentityEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		replacementKubeletIdentityResourceIDEnv,
		replacementKubeletIdentityClientIDEnv,
		replacementKubeletIdentityPrincipalIDEnv,
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

func TestReplacementKubeletIdentityFromEnvironmentAbsent(t *testing.T) {
	clearReplacementKubeletIdentityEnvironment(t)
	identity, configured, err := replacementKubeletIdentityFromEnvironment()
	if err != nil {
		t.Fatalf("load absent fixture: %v", err)
	}
	if configured || identity != nil {
		t.Fatalf("fixture = (%v, %t), want absent", identity, configured)
	}
}

func TestReplacementKubeletIdentityFromEnvironmentRejectsPartial(t *testing.T) {
	clearReplacementKubeletIdentityEnvironment(t)
	t.Setenv(replacementKubeletIdentityResourceIDEnv, "/subscriptions/sub/resourceGroups/node-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/replacement")
	if _, _, err := replacementKubeletIdentityFromEnvironment(); err == nil {
		t.Fatal("expected partial replacement identity fixture to fail")
	}
}

func TestReplacementKubeletIdentityFromEnvironmentComplete(t *testing.T) {
	clearReplacementKubeletIdentityEnvironment(t)
	resourceID := "/subscriptions/sub/resourceGroups/node-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/replacement"
	t.Setenv(replacementKubeletIdentityResourceIDEnv, resourceID)
	t.Setenv(replacementKubeletIdentityClientIDEnv, "client-id")
	t.Setenv(replacementKubeletIdentityPrincipalIDEnv, "principal-id")

	identity, configured, err := replacementKubeletIdentityFromEnvironment()
	if err != nil {
		t.Fatalf("load complete fixture: %v", err)
	}
	if !configured || identity == nil || identity.Properties == nil {
		t.Fatalf("fixture = (%v, %t), want configured identity", identity, configured)
	}
	if identity.ID == nil || *identity.ID != resourceID {
		t.Fatalf("resource ID = %v, want %q", identity.ID, resourceID)
	}
	if identity.Properties.ClientID == nil || *identity.Properties.ClientID != "client-id" {
		t.Fatalf("client ID = %v, want client-id", identity.Properties.ClientID)
	}
	if identity.Properties.PrincipalID == nil || *identity.Properties.PrincipalID != "principal-id" {
		t.Fatalf("principal ID = %v, want principal-id", identity.Properties.PrincipalID)
	}
}
