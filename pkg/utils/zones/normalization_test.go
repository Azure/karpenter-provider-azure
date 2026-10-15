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

package zones_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestRegisterCSIZoneNormalization(t *testing.T) {
	g := NewWithT(t)

	// Snapshot and restore the global maps so this test cannot leak into others in the package.
	origLabels, origValues := karpv1.NormalizedLabels, karpv1.NormalizedLabelValues
	t.Cleanup(func() {
		karpv1.NormalizedLabels, karpv1.NormalizedLabelValues = origLabels, origValues
	})
	karpv1.NormalizedLabels = map[string]string{"other.provider/zone": corev1.LabelTopologyZone}
	karpv1.NormalizedLabelValues = nil

	zones.RegisterCSIZoneNormalization()

	// Every Azure CSI zone key is aliased onto the well-known zone label.
	for _, label := range []string{
		zones.LabelAzureDiskCSIZone,
		zones.LabelAzureElasticSANCSIZone,
	} {
		g.Expect(karpv1.NormalizedLabels).To(HaveKeyWithValue(label, corev1.LabelTopologyZone), label)
	}
	g.Expect(karpv1.NormalizedLabels).To(HaveKeyWithValue("other.provider/zone", corev1.LabelTopologyZone), "pre-existing entries are preserved")
	g.Expect(karpv1.NormalizedLabelValues).To(HaveKeyWithValue(corev1.LabelTopologyZone, HaveKeyWithValue("", zones.Regional)))

	// Registering again is idempotent.
	before := len(karpv1.NormalizedLabels)
	zones.RegisterCSIZoneNormalization()
	g.Expect(karpv1.NormalizedLabels).To(HaveLen(before))
}

func TestCSIZoneRequirementsNormalize(t *testing.T) {
	// What a PV's nodeAffinity term looks like after csi-provisioner stamps the driver's topology
	// onto it, and what karpenter must turn it into to be able to provision a node.
	tc := []struct {
		testName       string
		key            string
		values         []string
		expectedValues []string
	}{
		{
			testName:       "Azure Disk CSI zonal",
			key:            zones.LabelAzureDiskCSIZone,
			values:         []string{"region-1"},
			expectedValues: []string{"region-1"},
		},
		{
			testName:       "Azure Disk CSI non-zonal",
			key:            zones.LabelAzureDiskCSIZone,
			values:         []string{""},
			expectedValues: []string{zones.Regional},
		},
		{
			testName:       "Elastic SAN CSI zonal",
			key:            zones.LabelAzureElasticSANCSIZone,
			values:         []string{"region-3"},
			expectedValues: []string{"region-3"},
		},
		{
			testName:       "Elastic SAN CSI non-zonal",
			key:            zones.LabelAzureElasticSANCSIZone,
			values:         []string{""},
			expectedValues: []string{zones.Regional},
		},
	}

	origLabels, origValues := karpv1.NormalizedLabels, karpv1.NormalizedLabelValues
	t.Cleanup(func() {
		karpv1.NormalizedLabels, karpv1.NormalizedLabelValues = origLabels, origValues
	})
	zones.RegisterCSIZoneNormalization()

	for _, c := range tc {
		g := NewWithT(t)
		// NewRequirementWithFlexibility mutates the values slice it is given; copy to keep the table intact.
		values := append([]string(nil), c.values...)
		req := scheduling.NewRequirement(c.key, corev1.NodeSelectorOpIn, values...)
		g.Expect(req.Key).To(Equal(corev1.LabelTopologyZone), c.testName)
		g.Expect(req.Values()).To(ConsistOf(c.expectedValues), c.testName)
	}
}
