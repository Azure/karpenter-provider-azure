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

package v1alpha2

import (
	"context"
	"fmt"
	"regexp"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"knative.dev/pkg/apis"
)

var (
	SubscriptionShape               = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)
	ComputeGalleryImageVersionRegex = regexp.MustCompile(`(?i)/subscriptions/` + SubscriptionShape.String() + `/resourceGroups/[\w-]+/providers/Microsoft\.Compute/galleries/[\w-]+/images/[\w-]+/versions/[\d.]+`)
	CommunityImageVersionRegex      = regexp.MustCompile(`(?i)/CommunityGalleries/[\w-]+/images/[\w-]+/versions/[\d.]+`)
)

func (in *AKSNodeClass) SupportedVerbs() []admissionregistrationv1.OperationType {
	return []admissionregistrationv1.OperationType{
		admissionregistrationv1.Create,
		admissionregistrationv1.Update,
	}
}

func IsComputeGalleryImageID(imageID string) bool {
	return ComputeGalleryImageVersionRegex.MatchString(imageID)
}

func (in *AKSNodeClass) Validate(ctx context.Context) (errs *apis.FieldError) {
	//if apis.IsInUpdate(ctx) {
	//	original := apis.GetBaseline(ctx).(*AKSNodeClass)
	//	errs = in.validateImmutableFields(original)
	//}
	return errs.Also(
		apis.ValidateObjectMetadata(in).ViaField("metadata"),
		in.Spec.validate(ctx).ViaField("spec"),
	)
}

func (in *AKSNodeClassSpec) validate(_ context.Context) (errs *apis.FieldError) {
	return errs.Also(
		in.validateImageID(),
	)
}

func (in *AKSNodeClassSpec) validateImageID() (errs *apis.FieldError) {
	if in.IsEmptyImageID() || ComputeGalleryImageVersionRegex.MatchString(*in.ImageID) || CommunityImageVersionRegex.MatchString(*in.ImageID) {
		return nil
	}
	return apis.ErrInvalidValue(fmt.Sprintf(
		"the provided image ID: '%s' is invalid because it doesn't match the expected format", *in.ImageID), "ImageID")
}
