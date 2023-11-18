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

package integration_test

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/karpenter-core/pkg/test"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
)

// This test requires the Azure Disk CSI driver to be installed
var _ = Describe("Dynamic PVC", func() {
	// DISABLED by charliedmcb.
	// TODO: need to clean up this test suite, and any bugs to reduce flakiness
	XIt("should run a pod with a dynamic persistent volume", func() {
		// Ensure that the Azure Disk driver is installed, or we can't run the test.
		var ds appsv1.DaemonSet
		if err := env.Client.Get(env.Context, client.ObjectKey{
			Namespace: "kube-system",
			Name:      "csi-azuredisk-node",
		}, &ds); err != nil {
			if errors.IsNotFound(err) {
				Skip(fmt.Sprintf("skipping dynamic PVC test due to missing Azure Disk driver %s", err))
			} else {
				Fail(fmt.Sprintf("determining Azure Disk driver status, %s", err))
			}
		}

		storageClassName := lo.ToPtr("azuredisk-sc-test")
		bindMode := storagev1.VolumeBindingWaitForFirstConsumer
		sc := test.StorageClass(test.StorageClassOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name: *storageClassName,
			},
			Provisioner:       lo.ToPtr("disk.csi.azure.com"),
			VolumeBindingMode: &bindMode,
		})

		pvc := test.PersistentVolumeClaim(test.PersistentVolumeClaimOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name: "azuredisk-claim",
			},
			StorageClassName: storageClassName,
			Resources:        v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("5Gi")}},
		})

		pod := test.Pod(test.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(nodeClass, nodePool, sc, pvc, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
		env.ExpectDeleted(pod)
	})
})

var _ = Describe("Static PVC", func() {
	It("should run a pod with a static persistent volume using Azure File", func() {
		storageClassName := lo.ToPtr("azurefile-test")
		bindMode := storagev1.VolumeBindingImmediate
		sc := test.StorageClass(test.StorageClassOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name: *storageClassName,
			},
			Provisioner:       lo.ToPtr("file.csi.azure.com"),
			VolumeBindingMode: &bindMode,
		})

		pv := test.PersistentVolume(test.PersistentVolumeOptions{
			ObjectMeta:       metav1.ObjectMeta{Name: "azurefile-test-volume"},
			StorageClassName: *storageClassName,
		})

		// Set up Azure File source
		pv.Spec.AzureFile = &v1.AzureFilePersistentVolumeSource{
			SecretName: "azure-secret", // Should have Azure Storage Account Name and Key
			ShareName:  "myshare",      // Name of the Azure File Share
			ReadOnly:   false,
		}
		pv.Spec.CSI = nil

		pvc := test.PersistentVolumeClaim(test.PersistentVolumeClaimOptions{
			ObjectMeta: metav1.ObjectMeta{
				Name: "azurefile-claim",
			},
			StorageClassName: storageClassName,
			VolumeName:       pv.Name,
			Resources:        v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("5Gi")}},
		})

		pod := test.Pod(test.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(nodeClass, nodePool, sc, pv, pvc, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
		env.ExpectDeleted(pod)
	})
})
