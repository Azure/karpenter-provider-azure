// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

const SecurityPatchFallbackAnnotation = "karpenter.azure.com/securitypatch-fallback"

// Image selection values are persisted in NodeClaim annotations and surfaced in events.
const (
	ImageSelectionSecurityPatch                      = "SecurityPatch"
	ImageSelectionStandardFallback                   = "StandardImageFallback"
	ImageSelectionReasonImageUnavailable             = "ImageUnavailable"
	ImageSelectionReasonCatalogUnavailable           = "CatalogUnavailable"
	ImageSelectionReasonNoCompatibleCapturedImage    = "NoCompatibleCapturedImage"
	ImageSelectionReasonExistingMachine              = "ExistingMachine"
	ImageSelectionReasonBackendCapabilityUnavailable = "BackendCapabilityUnavailable"
)

// SecurityPatchImageRejected is emitted only for an explicit rejection by the
// initial PUT, never for an ambiguous transport error or an accepted LRO failure.
type SecurityPatchImageRejected struct{ Err error }

// preserveMachineError prevents cleanup from deleting a potentially accepted
// Machine when its state or the outcome of a request is unknown.
type preserveMachineError struct{ error }

func (e *SecurityPatchImageRejected) Error() string {
	return fmt.Sprintf("captured image rejected: %v", e.Err)
}
func (e *SecurityPatchImageRejected) Unwrap() error { return e.Err }

func rejectedSecurityPatchImage(securityPatch bool, machine *armcontainerservice.Machine, err *offerings.HandlableError) bool {
	return securityPatch && err.Code == "SecurityVHDNotFound" && imagefamily.IsSecurityPatchVersion(lo.FromPtr(machine.Properties.NodeImageVersion))
}

func (p *DefaultAKSMachineProvider) classifyCreateRequestError(ctx context.Context, template *armcontainerservice.Machine, name string, instanceType *corecloudprovider.InstanceType, zone, capacityType, reservationGroupID string, err error) error {
	var response *azcore.ResponseError
	if options.FromContext(ctx).IsSecurityPatchChannel() && (!errors.As(err, &response) || response.StatusCode >= 500 || response.StatusCode == 408) {
		return &preserveMachineError{err}
	}
	if he := offerings.ErrorToHandlableError(err); he != nil {
		if rejectedSecurityPatchImage(options.FromContext(ctx).IsSecurityPatchChannel(), template, he) {
			return &SecurityPatchImageRejected{Err: he}
		}
		return p.handleMachineBeginCreateError(ctx, name, instanceType, zone, capacityType, reservationGroupID, he)
	}
	return fmt.Errorf("failed to begin create AKS machine %q, unhandled error: %w", name, err)
}

// selectSecurityPatchImage operates on the already resolved standard definition.
// It does not change scheduling, hardware, runtime, security settings or status.images.
// The budget includes waiting for a lookup slot and every catalog page. No result
// is retained across create calls. The parent context still controls the PUT.
func (p *DefaultAKSMachineProvider) selectSecurityPatchImage(ctx context.Context, standardNIV, forcedFallback string) (string, string, error) {
	return p.selectSecurityPatchImageWithTimeout(ctx, standardNIV, forcedFallback, 5*time.Second)
}

func (p *DefaultAKSMachineProvider) selectSecurityPatchImageWithTimeout(ctx context.Context, standardNIV, forcedFallback string, timeout time.Duration) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if forcedFallback == ImageSelectionReasonImageUnavailable {
		return standardNIV, forcedFallback, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case p.securityPatchLookups <- struct{}{}:
		defer func() { <-p.securityPatchLookups }()
	case <-lookupCtx.Done():
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		return standardNIV, ImageSelectionReasonCatalogUnavailable, nil
	}
	versions, err := p.azClient.NodeImageVersionsClient.List(imagefamily.WithSecurityPatchCatalog(lookupCtx), p.aksMachinesPoolLocation)
	if parentErr := ctx.Err(); parentErr != nil {
		return "", "", parentErr
	}
	if errors.Is(err, imagefamily.ErrCapturedImagesOnlyNotAcknowledged) {
		return standardNIV, ImageSelectionReasonBackendCapabilityUnavailable, nil
	}
	if err != nil || lookupCtx.Err() != nil {
		log.FromContext(ctx).Info("captured image discovery unavailable; preserving standard selection", "error", err, "standardNIV", standardNIV)
		return standardNIV, ImageSelectionReasonCatalogUnavailable, nil
	}
	selected := selectCapturedVersion(standardNIV, versions)
	if selected == "" {
		return standardNIV, ImageSelectionReasonNoCompatibleCapturedImage, nil
	}
	return selected, ImageSelectionSecurityPatch, nil
}

func selectCapturedVersion(standardNIV string, versions []*armcontainerservice.NodeImageVersion) string {
	// Standard NIV is gallery-definition-version. Neither gallery nor definition
	// in this contract contains a hyphen. Do not guess when the baseline is invalid.
	parts := strings.Split(standardNIV, "-")
	if len(parts) != 3 {
		return ""
	}
	matches := []*armcontainerservice.NodeImageVersion{}
	for _, version := range versions {
		if version == nil || lo.FromPtr(version.OS) != parts[0] || lo.FromPtr(version.SKU) != parts[1] {
			continue
		}
		name := parts[0] + "-" + parts[1] + "-" + lo.FromPtr(version.Version)
		if !imagefamily.IsSecurityPatchVersion(name) {
			continue
		}
		if fullName := lo.FromPtr(version.FullName); fullName != "" && fullName != name {
			continue
		}
		matches = append(matches, version)
	}
	latest := imagefamily.FilteredNodeImages(matches)
	if len(latest) != 1 {
		return ""
	}
	return parts[0] + "-" + parts[1] + "-" + lo.FromPtr(latest[0].Version)
}
