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

	corev1 "k8s.io/api/core/v1"
)

func TestHasCompatibleNetworkSettings(t *testing.T) {
	t.Run("managed NAP does not read self-hosted settings", func(t *testing.T) {
		called := false
		compatible := hasCompatibleNetworkSettings(false, func() []corev1.EnvVar {
			called = true
			return nil
		})
		if !compatible {
			t.Fatal("managed NAP networking fixture should be accepted")
		}
		if called {
			t.Fatal("managed NAP attempted to read the self-hosted Karpenter Deployment")
		}
	})

	t.Run("self-hosted requires overlay", func(t *testing.T) {
		compatible := hasCompatibleNetworkSettings(true, func() []corev1.EnvVar {
			return []corev1.EnvVar{{Name: "NETWORK_PLUGIN_MODE", Value: "overlay"}}
		})
		if !compatible {
			t.Fatal("self-hosted overlay settings should be accepted")
		}
	})

	t.Run("self-hosted rejects incompatible plugin", func(t *testing.T) {
		compatible := hasCompatibleNetworkSettings(true, func() []corev1.EnvVar {
			return []corev1.EnvVar{
				{Name: "NETWORK_PLUGIN", Value: "kubenet"},
				{Name: "NETWORK_PLUGIN_MODE", Value: "overlay"},
			}
		})
		if compatible {
			t.Fatal("self-hosted incompatible plugin should be rejected")
		}
	})
}
