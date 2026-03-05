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

package nodeclaim

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
)

func TestAKSNodeClassFromAzureNodeClass_BasicFields(t *testing.T) {
	g := NewWithT(t)

	azureNC := &v1alpha1.AzureNodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nc",
			Namespace: "default",
			Labels:    map[string]string{"env": "test"},
			Annotations: map[string]string{
				"note": "hello",
			},
		},
		Spec: v1alpha1.AzureNodeClassSpec{
			VNETSubnetID: lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"),
			OSDiskSizeGB: lo.ToPtr(int32(128)),
			Tags:         map[string]string{"team": "infra"},
		},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Name).To(Equal("test-nc"))
	g.Expect(aksNC.Namespace).To(Equal("default"))
	g.Expect(aksNC.Labels).To(HaveKeyWithValue("env", "test"))
	g.Expect(aksNC.Annotations).To(HaveKeyWithValue("note", "hello"))
	g.Expect(aksNC.Spec.VNETSubnetID).NotTo(BeNil())
	g.Expect(*aksNC.Spec.VNETSubnetID).To(Equal(*azureNC.Spec.VNETSubnetID))
	g.Expect(aksNC.Spec.OSDiskSizeGB).NotTo(BeNil())
	g.Expect(*aksNC.Spec.OSDiskSizeGB).To(Equal(int32(128)))
	g.Expect(aksNC.Spec.Tags).To(HaveKeyWithValue("team", "infra"))
}

func TestAKSNodeClassFromAzureNodeClass_Security(t *testing.T) {
	g := NewWithT(t)

	azureNC := &v1alpha1.AzureNodeClass{
		Spec: v1alpha1.AzureNodeClassSpec{
			Security: &v1alpha1.AzureNodeClassSecurity{
				EncryptionAtHost: lo.ToPtr(true),
			},
		},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Spec.Security).NotTo(BeNil())
	g.Expect(aksNC.Spec.Security.EncryptionAtHost).NotTo(BeNil())
	g.Expect(*aksNC.Spec.Security.EncryptionAtHost).To(BeTrue())
}

func TestAKSNodeClassFromAzureNodeClass_NilSecurity(t *testing.T) {
	g := NewWithT(t)

	azureNC := &v1alpha1.AzureNodeClass{
		Spec: v1alpha1.AzureNodeClassSpec{},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Spec.Security).To(BeNil())
}

func TestAKSNodeClassFromAzureNodeClass_ImageID(t *testing.T) {
	g := NewWithT(t)

	imageID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/galleries/gallery/images/myimage/versions/1.0.0"
	azureNC := &v1alpha1.AzureNodeClass{
		Spec: v1alpha1.AzureNodeClassSpec{
			ImageID: &imageID,
		},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Spec.ImageID).NotTo(BeNil())
	g.Expect(*aksNC.Spec.ImageID).To(Equal(imageID))
}

func TestAKSNodeClassFromAzureNodeClass_UserData(t *testing.T) {
	g := NewWithT(t)

	userData := "#!/bin/bash\necho hello"
	azureNC := &v1alpha1.AzureNodeClass{
		Spec: v1alpha1.AzureNodeClassSpec{
			UserData: &userData,
		},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Spec.UserData).NotTo(BeNil())
	g.Expect(*aksNC.Spec.UserData).To(Equal(userData))
}

func TestAKSNodeClassFromAzureNodeClass_ManagedIdentities(t *testing.T) {
	g := NewWithT(t)

	azureNC := &v1alpha1.AzureNodeClass{
		Spec: v1alpha1.AzureNodeClassSpec{
			ManagedIdentities: []string{
				"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1",
				"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id2",
			},
		},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Spec.ManagedIdentities).To(HaveLen(2))
	g.Expect(aksNC.Spec.ManagedIdentities[0]).To(ContainSubstring("id1"))
	g.Expect(aksNC.Spec.ManagedIdentities[1]).To(ContainSubstring("id2"))
}

func TestAKSNodeClassFromAzureNodeClass_NilUserDataAndIdentities(t *testing.T) {
	g := NewWithT(t)

	azureNC := &v1alpha1.AzureNodeClass{
		Spec: v1alpha1.AzureNodeClassSpec{},
	}

	aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

	g.Expect(aksNC.Spec.UserData).To(BeNil())
	g.Expect(aksNC.Spec.ManagedIdentities).To(BeNil())
}

func TestGetVMName_ValidProviderID(t *testing.T) {
	g := NewWithT(t)

	providerID := "azure:///subscriptions/sub-123/resourceGroups/MC_rg/providers/Microsoft.Compute/virtualMachines/aks-my-vm"
	vmName, err := GetVMName(providerID)

	g.Expect(err).To(BeNil())
	g.Expect(vmName).To(Equal("aks-my-vm"))
}

func TestGetVMName_InvalidProviderID(t *testing.T) {
	g := NewWithT(t)

	_, err := GetVMName("invalid-provider-id")

	g.Expect(err).NotTo(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("parsing vm name"))
}
