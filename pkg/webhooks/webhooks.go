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

package webhooks

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	knativeinjection "knative.dev/pkg/injection"
	"knative.dev/pkg/webhook/resourcesemantics"
	"knative.dev/pkg/webhook/resourcesemantics/defaulting"
	"knative.dev/pkg/webhook/resourcesemantics/validation"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
)

func NewWebhooks() []knativeinjection.ControllerConstructor {
	return []knativeinjection.ControllerConstructor{
		NewCRDDefaultingWebhook,
		NewCRDValidationWebhook,
	}
}

func NewCRDDefaultingWebhook(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	return defaulting.NewAdmissionController(ctx,
		"defaulting.webhook.karpenter.azure.com",
		"/default/karpenter.azure.com",
		Resources,
		func(ctx context.Context) context.Context { return ctx },
		true,
	)
}

func NewCRDValidationWebhook(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	return validation.NewAdmissionController(ctx,
		"validation.webhook.karpenter.azure.com",
		"/validate/karpenter.azure.com",
		Resources,
		func(ctx context.Context) context.Context { return ctx },
		true,
	)
}

var Resources = map[schema.GroupVersionKind]resourcesemantics.GenericCRD{
	v1alpha2.SchemeGroupVersion.WithKind("AKSNodeClass"): &v1alpha2.AKSNodeClass{},
	// corev1alpha5.SchemeGroupVersion.WithKind("Provisioner"): &v1alpha5.Provisioner{},
}
