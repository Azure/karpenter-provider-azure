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

package utils

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// NewTerminatingResourceError returns a NotFound error indicating that the resource is terminating.
// This is useful for resources where termination should be treated as not found.
func NewTerminatingResourceError(gr schema.GroupResource, name string) *errors.StatusError {
	err := errors.NewNotFound(gr, name)
	err.ErrStatus.Message = fmt.Sprintf("%s %q is terminating, treating as not found", gr.String(), name)
	return err
}
