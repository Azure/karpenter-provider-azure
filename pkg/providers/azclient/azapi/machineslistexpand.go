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

package azapi

import "context"

type aksMachineVMStateExpansionKey struct{}

// WithAKSMachineVMStateExpansion marks Machine LIST requests to include the VM state field.
func WithAKSMachineVMStateExpansion(ctx context.Context) context.Context {
	return context.WithValue(ctx, aksMachineVMStateExpansionKey{}, struct{}{})
}

// IsAKSMachineVMStateExpansionEnabled returns whether Machine LIST requests should include the VM state field.
func IsAKSMachineVMStateExpansionEnabled(ctx context.Context) bool {
	_, ok := ctx.Value(aksMachineVMStateExpansionKey{}).(struct{})
	return ok
}
