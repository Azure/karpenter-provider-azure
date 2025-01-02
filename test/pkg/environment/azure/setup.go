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
	//nolint:revive,stylecheck
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
)

var (
	CleanableObjects = []client.Object{
		&v1alpha2.AKSNodeClass{},
	}
)

func (env *Environment) BeforeEach() {
	env.Environment.BeforeEach()
}

func (env *Environment) Cleanup() {
	env.Environment.Cleanup()
	env.Environment.CleanupObjects(CleanableObjects...)
}

func (env *Environment) AfterEach() {
	fmt.Println("##[group]    E2E SUITE: LOG DUMP")
	defer fmt.Println("##[endgroup]")
	env.Environment.AfterEach()
}
