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

package zones

import (
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// RegisterCSIZoneNormalization wires the Azure Disk CSI zone label + value
// normalization into karpenter's global normalization maps.
//
// It is safe to call from multiple init() functions (production operator,
// pkg/test env, and the e2e env under test/pkg/environment) — each call
// merges into the existing maps rather than overwriting, so upstream and
// other providers' entries are preserved. The helper is also nil-safe:
// NormalizedLabelValues is initialized to a non-nil map upstream, but we
// defensively guard against a future change or an out-of-order init.
//
// Value normalization: the Azure Disk CSI driver emits "" for non-zonal
// topology while cloud-provider-azure uses "0" (fault domain) for regional
// VMs, so we translate "" -> zones.Regional on the normalized zone label.
func RegisterCSIZoneNormalization() {
	// Label-key normalization: alias the CSI zone label onto the well-known
	// topology zone label. lo.Assign returns a new map with a nil-safe merge.
	karpv1.NormalizedLabels = lo.Assign(
		karpv1.NormalizedLabels,
		map[string]string{"topology.disk.csi.azure.com/zone": corev1.LabelTopologyZone},
	)

	// Value normalization: merge into any existing per-label map instead of
	// replacing it, so we don't clobber upstream/other-provider mappings.
	if karpv1.NormalizedLabelValues == nil {
		karpv1.NormalizedLabelValues = map[string]map[string]string{}
	}
	karpv1.NormalizedLabelValues[corev1.LabelTopologyZone] = lo.Assign(
		karpv1.NormalizedLabelValues[corev1.LabelTopologyZone],
		map[string]string{"": Regional},
	)
}
