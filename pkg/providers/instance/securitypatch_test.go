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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/samber/lo"
)

type capturedCatalogFunc func(context.Context, string) ([]*armcontainerservice.NodeImageVersion, error)

func (f capturedCatalogFunc) List(ctx context.Context, location string) ([]*armcontainerservice.NodeImageVersion, error) {
	return f(ctx, location)
}

func TestCreationTimeSecurityPatchSelection(t *testing.T) {
	standard := "AKSUbuntu-2404gen2containerd-202609.23.0"
	image := func(sku, version string) *armcontainerservice.NodeImageVersion {
		return &armcontainerservice.NodeImageVersion{OS: lo.ToPtr("AKSUbuntu"), SKU: lo.ToPtr(sku), Version: lo.ToPtr(version)}
	}
	for _, tc := range []struct {
		name         string
		images       []*armcontainerservice.NodeImageVersion
		err          error
		forced       string
		want, reason string
	}{
		{name: "exact definition and newest patch", images: []*armcontainerservice.NodeImageVersion{image("2404containerd", "202609.23.0-2026.09.30"), image("2404gen2containerd", "202609.23.0-2026.09.25"), image("2404gen2containerd", "202608.01.0-2026.09.26")}, want: "AKSUbuntu-2404gen2containerd-202608.01.0-2026.09.26", reason: ImageSelectionSecurityPatch},
		{name: "missing definition", images: []*armcontainerservice.NodeImageVersion{image("2404containerd", "202609.23.0-2026.09.30")}, want: standard, reason: ImageSelectionReasonNoCompatibleCapturedImage},
		{name: "empty catalog", want: standard, reason: ImageSelectionReasonNoCompatibleCapturedImage},
		{name: "ignore standard response from old server", images: []*armcontainerservice.NodeImageVersion{image("2404gen2containerd", "202609.23.0")}, want: standard, reason: ImageSelectionReasonNoCompatibleCapturedImage},
		{name: "failed discovery", err: errors.New("unavailable"), want: standard, reason: ImageSelectionReasonCatalogUnavailable},
		{name: "previous explicit rejection", forced: ImageSelectionReasonImageUnavailable, want: standard, reason: ImageSelectionReasonImageUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := &DefaultAKSMachineProvider{securityPatchLookups: make(chan struct{}, 4), azClient: &azclient.AZClient{NodeImageVersionsClient: capturedCatalogFunc(func(ctx context.Context, _ string) ([]*armcontainerservice.NodeImageVersion, error) {
				calls++
				if !imagefamily.IsSecurityPatchCatalog(ctx) {
					t.Fatal("missing captured request marker")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing lookup deadline")
				}
				return tc.images, tc.err
			})}}
			for i := 0; i < 2; i++ {
				got, reason, err := p.selectSecurityPatchImage(context.Background(), standard, tc.forced)
				if err != nil || got != tc.want || reason != tc.reason {
					t.Fatalf("got %s %s %v", got, reason, err)
				}
			}
			expectedCalls := 2
			if tc.forced != "" {
				expectedCalls = 0
			}
			if calls != expectedCalls {
				t.Fatalf("got %d catalog calls, want %d", calls, expectedCalls)
			}
		})
	}
}

func TestCreationTimeSelectionCancellationAndConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &DefaultAKSMachineProvider{securityPatchLookups: make(chan struct{}, 1)}
	if _, _, err := p.selectSecurityPatchImage(ctx, "standard", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	p.securityPatchLookups <- struct{}{}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := p.selectSecurityPatchImage(ctx, "standard", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost queued cancellation: %v", err)
	}
}

func TestCapturedLookupTimeoutFallsBackWithoutCancellingCreate(t *testing.T) {
	p := &DefaultAKSMachineProvider{securityPatchLookups: make(chan struct{}, 1), azClient: &azclient.AZClient{NodeImageVersionsClient: capturedCatalogFunc(func(ctx context.Context, _ string) ([]*armcontainerservice.NodeImageVersion, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})}}
	ctx := context.Background()
	got, reason, err := p.selectSecurityPatchImageWithTimeout(ctx, "AKSUbuntu-2404gen2containerd-202609.23.0", "", 10*time.Millisecond)
	if err != nil || reason != ImageSelectionReasonCatalogUnavailable || got != "AKSUbuntu-2404gen2containerd-202609.23.0" || ctx.Err() != nil {
		t.Fatalf("unexpected timeout behavior: %s %s %v", got, reason, err)
	}
	if len(p.securityPatchLookups) != 0 {
		t.Fatal("lookup slot leaked")
	}
}

func TestQueuedCapturedLookupTimeoutFallsBack(t *testing.T) {
	p := &DefaultAKSMachineProvider{securityPatchLookups: make(chan struct{}, 1)}
	p.securityPatchLookups <- struct{}{}
	ctx := context.Background()
	standard := "AKSUbuntu-2404gen2containerd-202609.23.0"
	got, reason, err := p.selectSecurityPatchImageWithTimeout(ctx, standard, "", 10*time.Millisecond)
	if err != nil || got != standard || reason != ImageSelectionReasonCatalogUnavailable || ctx.Err() != nil {
		t.Fatalf("unexpected queued timeout: %s %s %v", got, reason, err)
	}
	if len(p.securityPatchLookups) != 1 {
		t.Fatal("queued timeout released another lookup's slot")
	}
}

func TestOnlyCapturedExplicitRejectionQualifiesForFallback(t *testing.T) {
	for _, tc := range []struct {
		version, code string
		want          bool
	}{
		{"AKSUbuntu-2404gen2containerd-202609.23.0-2026.09.25", "SecurityVHDNotFound", true},
		{"AKSUbuntu-2404gen2containerd-202609.23.0", "SecurityVHDNotFound", false},
		{"AKSUbuntu-2404gen2containerd-202609.23.0-2026.09.25", "AuthorizationFailed", false},
	} {
		machine := &armcontainerservice.Machine{Properties: &armcontainerservice.MachineProperties{NodeImageVersion: lo.ToPtr(tc.version)}}
		if got := rejectedSecurityPatchImage(true, machine, &offerings.HandlableError{Code: tc.code}); got != tc.want {
			t.Fatalf("%s %s: %v", tc.version, tc.code, got)
		}
		if rejectedSecurityPatchImage(false, machine, &offerings.HandlableError{Code: tc.code}) {
			t.Fatal("image fallback must not apply outside the SecurityPatch channel")
		}
	}
}
