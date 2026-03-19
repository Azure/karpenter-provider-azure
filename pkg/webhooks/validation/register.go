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

package validation

import (
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

const (
	// NodePoolValidationPath is the path for the NodePool validation webhook.
	NodePoolValidationPath = "/validate-karpenter-sh-v1-nodepool"
)

// RegisterWebhooks registers all validating webhooks with the given manager.
// This should be called during operator startup.
//
// Prerequisites:
//   - The manager must have webhook serving configured (TLS certs, port, etc.)
//   - A ValidatingWebhookConfiguration must be deployed that points to these paths.
//
// For NAP (managed Karpenter), the webhook configuration is managed by the AKS RP.
// For self-hosted Karpenter, users need to deploy the ValidatingWebhookConfiguration
// and provide TLS certificates (e.g., via cert-manager).
func RegisterWebhooks(mgr manager.Manager) {
	webhookServer := mgr.GetWebhookServer()

	webhookServer.Register(NodePoolValidationPath, &webhook.Admission{
		Handler: NewNodePoolValidator(mgr.GetScheme()),
	})
}
