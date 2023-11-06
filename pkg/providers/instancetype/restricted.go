// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

/*
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

package instancetype

import "k8s.io/apimachinery/pkg/util/sets"

var (
	RestrictedVMSizes = sets.New(
		"Standard_A0",
		"Standard_A1",
		"Standard_A1_v2",
		"Standard_B1s",
		"Standard_B1ms",
		"Standard_F1",
		"Standard_F1s",
		"Basic_A0",
		"Basic_A1",
		"Basic_A2",
		"Basic_A3",
		"Basic_A4",
	)
)
