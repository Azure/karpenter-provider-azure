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

package storage_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const (
	azureDiskCSIProvisioner = "disk.csi.azure.com"
	regionalUltraSSDRegion  = "westus"
	ultraSSDMountPath       = "/mnt/ultrassd"
)

var _ = Describe("UltraSSD", func() {
	It("should have UltraSSD disabled by default", func() {
		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Name).To(Equal(pods[0].Spec.NodeName))
		verifyUltraSSDOnNode(node, false)
		checkNodeLabels(node, false)
	})

	It("should disable UltraSSD when explicitly disabled", func() {
		nodePool = coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      v1beta1.LabelUltraSSD,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"false"},
		})

		deployment := coretest.Deployment(coretest.DeploymentOptions{Replicas: 1})
		env.ExpectCreated(nodeClass, nodePool, deployment)
		pods := env.EventuallyExpectHealthyDeployment(deployment)

		node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
		Expect(node.Name).To(Equal(pods[0].Spec.NodeName))
		verifyUltraSSDOnNode(node, false)
		checkNodeLabels(node, false)
	})

	It("should provision and mount an UltraSSD volume on a zonal node", Label("runner"), func() {
		if env.Region == regionalUltraSSDRegion {
			Skip(fmt.Sprintf("skipping zonal UltraSSD test in regional-only location %s", env.Region))
		}
		expectUltraSSDVolume(v1beta1.PlacementScopeZonal)
	})

	It("should provision and mount an UltraSSD volume on a regional node", Label("runner"), func() {
		if env.Region != regionalUltraSSDRegion {
			Skip(fmt.Sprintf("skipping regional UltraSSD test outside regional-only location %s", regionalUltraSSDRegion))
		}
		expectUltraSSDVolume(v1beta1.PlacementScopeRegional)
	})
})

func expectUltraSSDVolume(placementScope string) {
	GinkgoHelper()
	ensureAzureDiskCSI()

	nodePool = coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		Key:      v1beta1.LabelUltraSSD,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{"true"},
	})
	nodePool = coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		Key:      v1beta1.LabelPlacementScope,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{placementScope},
	})

	storageClass := coretest.StorageClass(coretest.StorageClassOptions{
		ObjectMeta:        metav1.ObjectMeta{Name: "ultrassd"},
		Provisioner:       new(azureDiskCSIProvisioner),
		VolumeBindingMode: new(storagev1.VolumeBindingWaitForFirstConsumer),
	})
	storageClass.Parameters = map[string]string{
		"cachingMode":       "None",
		"DiskIOPSReadWrite": "500",
		"DiskMBpsReadWrite": "100",
		"kind":              "managed",
		"skuName":           "UltraSSD_LRS",
	}
	pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
		StorageClassName: &storageClass.Name,
		Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse("4Gi"),
		}},
	})
	deployment := coretest.Deployment(coretest.DeploymentOptions{
		Replicas: 1,
		PodOptions: coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		},
	})
	Expect(deployment.Spec.Template.Spec.Volumes).To(HaveLen(1))
	// Mount the PVC so pod readiness proves the UltraSSD volume was attached and mounted, not only provisioned.
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
		Name:      deployment.Spec.Template.Spec.Volumes[0].Name,
		MountPath: ultraSSDMountPath,
	}}

	env.ExpectCreated(nodeClass, nodePool, storageClass, pvc, deployment)
	pods := env.EventuallyExpectHealthyDeployment(deployment)
	pv := env.EventuallyExpectPVCBound(pvc)

	Expect(pv.Spec.CSI).ToNot(BeNil())
	Expect(pv.Spec.CSI.Driver).To(Equal(azureDiskCSIProvisioner))
	Expect(pv.Spec.CSI.VolumeHandle).ToNot(BeEmpty())

	node := env.EventuallyExpectInitializedNodeCount("==", 1)[0]
	Expect(node.Name).To(Equal(pods[0].Spec.NodeName))
	checkNodeLabels(node, true)
	verifyUltraSSDOnNode(node, true)
	Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, placementScope))
	if placementScope == v1beta1.PlacementScopeRegional {
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelTopologyZone, zones.Regional))
	} else {
		Expect(node.Labels).To(HaveKey(corev1.LabelTopologyZone))
		Expect(node.Labels[corev1.LabelTopologyZone]).ToNot(Equal(zones.Regional))
	}
}

func ensureAzureDiskCSI() {
	GinkgoHelper()
	var daemonSet appsv1.DaemonSet
	if err := env.Client.Get(env.Context, client.ObjectKey{
		Namespace: "kube-system",
		Name:      "csi-azuredisk-node",
	}, &daemonSet); err != nil {
		if errors.IsNotFound(err) {
			Skip(fmt.Sprintf("skipping UltraSSD test due to missing Azure Disk driver: %s", err))
		}
		Fail(fmt.Sprintf("determining Azure Disk driver status: %s", err))
	}
}

func verifyUltraSSDOnNode(node *corev1.Node, expected bool) {
	GinkgoHelper()
	vm := env.GetVM(node.Name)
	Expect(vm.Properties).ToNot(BeNil())

	if expected {
		Expect(vm.Properties.AdditionalCapabilities).ToNot(BeNil())
		Expect(vm.Properties.AdditionalCapabilities.UltraSSDEnabled).ToNot(BeNil())
		Expect(*vm.Properties.AdditionalCapabilities.UltraSSDEnabled).To(BeTrue())
		return
	}

	if vm.Properties.AdditionalCapabilities == nil || vm.Properties.AdditionalCapabilities.UltraSSDEnabled == nil {
		return
	}
	Expect(*vm.Properties.AdditionalCapabilities.UltraSSDEnabled).To(BeFalse())
}

func checkNodeLabels(node *corev1.Node, expected bool) {
	GinkgoHelper()
	Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelUltraSSD, fmt.Sprint(expected)))

	if !expected {
		for key, value := range node.Labels {
			GinkgoWriter.Printf("Node label: %s=%s\n", key, value)
		}
	}
}
