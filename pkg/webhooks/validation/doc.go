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

// Package validation provides validating webhook handlers that enforce AKS Machine API
// constraints at admission time, preventing late-binding errors during node provisioning.
//
// This addresses the validation sync problem (karpenter-poc#1507): Karpenter CRD validation
// was previously a best-effort duplication of AKS Machine API validation rules, which led to
// drift and confusing errors at provisioning time. These webhooks bring the validation forward
// to admission time, giving users immediate feedback.
package validation
