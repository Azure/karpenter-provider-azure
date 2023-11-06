// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azure

import (
	//nolint:revive,stylecheck
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
)

var persistedSettings []v1.EnvVar
var persistedSettingsLegacy = &v1.ConfigMap{}

var (
	CleanableObjects = []client.Object{
		&v1alpha2.AKSNodeClass{},
	}
)

func (env *Environment) BeforeEach() {
	persistedSettings = env.ExpectSettings()
	persistedSettingsLegacy = env.ExpectSettingsLegacy()
	env.Environment.BeforeEach()
}

func (env *Environment) Cleanup() {
	env.Environment.Cleanup()
	env.Environment.CleanupObjects(CleanableObjects...)
}

func (env *Environment) AfterEach() {
	env.Environment.AfterEach()
	// Ensure we reset settings after collecting the controller logs
	env.ExpectSettingsReplaced(persistedSettings...)
	env.ExpectSettingsReplacedLegacy(persistedSettingsLegacy.Data)
}
