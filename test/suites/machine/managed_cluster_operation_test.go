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

package machine_test

import (
	"testing"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
)

func TestLatestKubernetesUpgradeVersion(t *testing.T) {
	version := func(value string) *containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem {
		return &containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem{KubernetesVersion: &value}
	}

	t.Run("selects highest semantic version", func(t *testing.T) {
		got, err := latestKubernetesUpgradeVersion([]*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem{
			version("1.35.7"),
			version("1.36.3"),
			version("1.35.10"),
		})
		if err != nil {
			t.Fatalf("select latest upgrade: %v", err)
		}
		if got != "1.36.3" {
			t.Fatalf("latest upgrade = %q, want 1.36.3", got)
		}
	})

	for _, tt := range []struct {
		name     string
		upgrades []*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem
	}{
		{name: "empty list"},
		{name: "nil item", upgrades: []*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem{nil}},
		{name: "nil version", upgrades: []*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem{{}}},
		{name: "invalid version", upgrades: []*containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem{version("not-a-version")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := latestKubernetesUpgradeVersion(tt.upgrades); err == nil {
				t.Fatal("expected invalid upgrade profile to return an error")
			}
		})
	}
}
