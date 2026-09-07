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

package imagefamily

import (
	"errors"
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// TestKubernetesVersionPinnedImageFamilyRegistry pins the invariant that
// kubernetesVersionPinnedImageFamilies is the only source of truth for pinned-family
// policy: for every registered entry, the predicate, the accepted/rejected version
// bounds, and the bounds reported on the typed error must all agree with the registry
// itself. Adding a family (or changing a bound) without keeping the predicate and the
// validator in step therefore fails here rather than silently diverging.
func TestKubernetesVersionPinnedImageFamilyRegistry(t *testing.T) {
	t.Parallel()

	NewWithT(t).Expect(kubernetesVersionPinnedImageFamilies).ToNot(BeEmpty())

	for imageFamily, policy := range kubernetesVersionPinnedImageFamilies {
		t.Run(imageFamily, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)
			for _, fips := range []bool{false, true} {
				nodeClass := testNodeClass(lo.ToPtr(imageFamily), fips)

				g.Expect(RequiresKubernetesVersionCompatibility(nodeClass)).To(BeTrue(),
					"registered family %q (fips=%t) must require the Kubernetes version", imageFamily, fips)

				maximumVersion, fipsApplied := policy.upperBound(fips)

				// The inclusive lower bound is accepted, anything below it is rejected.
				expectRegisteredBounds(g, nodeClass, imageFamily, policy, maximumVersion, fipsApplied,
					justBelowVersion(policy.minimumVersion))
				g.Expect(ValidateImageFamilyCompatibility(nodeClass, policy.minimumVersion.String())).To(Succeed(),
					"family %q (fips=%t) must accept its registered minimum version", imageFamily, fips)

				if maximumVersion == nil {
					// An unbounded family must stay accepted arbitrarily far above the minimum.
					unbounded := semver.Version{Major: policy.minimumVersion.Major, Minor: policy.minimumVersion.Minor + 100}
					g.Expect(ValidateImageFamilyCompatibility(nodeClass, unbounded.String())).To(Succeed(),
						"family %q (fips=%t) has no registered upper bound and must accept %s", imageFamily, fips, unbounded)
					continue
				}

				// The upper bound is exclusive: just below is accepted, the bound itself is not.
				g.Expect(ValidateImageFamilyCompatibility(nodeClass, justBelowVersion(*maximumVersion))).To(Succeed(),
					"family %q (fips=%t) must accept versions below its registered maximum", imageFamily, fips)
				expectRegisteredBounds(g, nodeClass, imageFamily, policy, maximumVersion, fipsApplied,
					maximumVersion.String())
			}
		})
	}

	t.Run("unpinned families are absent from the registry and never validated", func(t *testing.T) {
		t.Parallel()

		g := NewWithT(t)
		for _, imageFamily := range []*string{nil, lo.ToPtr(v1beta1.UbuntuImageFamily), lo.ToPtr(v1beta1.AzureLinuxImageFamily)} {
			_, found := kubernetesVersionPinnedImageFamilies[lo.FromPtr(imageFamily)]
			g.Expect(found).To(BeFalse(), "image family %q must not be registered as version pinned", lo.FromPtr(imageFamily))

			for _, fips := range []bool{false, true} {
				nodeClass := testNodeClass(imageFamily, fips)

				g.Expect(RequiresKubernetesVersionCompatibility(nodeClass)).To(BeFalse())
				g.Expect(ValidateImageFamilyCompatibility(nodeClass, "1.0.0")).To(Succeed())
			}
		}
	})
}

// expectRegisteredBounds asserts that kubernetesVersion is rejected, and that every
// bound reported by the typed error and its message is the one held in the registry.
func expectRegisteredBounds(
	g *WithT,
	nodeClass *v1beta1.AKSNodeClass,
	imageFamily string,
	policy kubernetesVersionPolicy,
	maximumVersion *semver.Version,
	fipsApplied bool,
	kubernetesVersion string,
) {
	err := ValidateImageFamilyCompatibility(nodeClass, kubernetesVersion)
	g.Expect(err).To(HaveOccurred(), "family %q must reject Kubernetes version %s", imageFamily, kubernetesVersion)

	var incompatibleErr *ImageFamilyKubernetesVersionIncompatibleError
	g.Expect(errors.As(err, &incompatibleErr)).To(BeTrue())
	g.Expect(incompatibleErr.RequestedImageFamily).To(Equal(imageFamily))
	g.Expect(incompatibleErr.KubernetesVersion).To(Equal(kubernetesVersion))
	g.Expect(incompatibleErr.FIPS).To(Equal(fipsApplied))
	g.Expect(incompatibleErr.MinimumVersion.String()).To(Equal(policy.minimumVersion.String()))
	if maximumVersion == nil {
		g.Expect(incompatibleErr.MaximumVersion).To(BeNil())
	} else {
		g.Expect(incompatibleErr.MaximumVersion).ToNot(BeNil())
		g.Expect(incompatibleErr.MaximumVersion.String()).To(Equal(maximumVersion.String()))
	}

	expectedRange := fmt.Sprintf(">= %s", policy.minimumVersion)
	if maximumVersion != nil {
		expectedRange = fmt.Sprintf("%s and < %s", expectedRange, maximumVersion)
	}
	g.Expect(err.Error()).To(ContainSubstring("supported range is %s", expectedRange))
}

func testNodeClass(imageFamily *string, fips bool) *v1beta1.AKSNodeClass {
	nodeClass := &v1beta1.AKSNodeClass{
		Spec: v1beta1.AKSNodeClassSpec{
			ImageFamily: imageFamily,
		},
	}
	if fips {
		nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)
	}
	return nodeClass
}

// justBelowVersion returns the closest version below v that semver orders strictly
// lower: any prerelease of v precedes v itself.
func justBelowVersion(v semver.Version) string {
	return fmt.Sprintf("%s-alpha", v)
}
