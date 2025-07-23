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

package common

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CoerceToObject converts a slice of objects that implement client.Object to a slice of client.Object
// This is useful for passing multiple objects to functions that expect a slice of client.Object
// and is required because Go slices are not covariant.
func CoerceToObject[T client.Object](objs ...T) []client.Object {
	coerced := make([]client.Object, len(objs))
	for i, obj := range objs {
		coerced[i] = obj
	}
	return coerced
}
