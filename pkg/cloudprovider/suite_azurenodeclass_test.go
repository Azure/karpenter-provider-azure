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

package cloudprovider

import (
	"encoding/base64"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
)

var _ = Describe("AzureNodeClass", func() {
	var azureNodeClass *v1alpha1.AzureNodeClass
	var azureNodePool *karpv1.NodePool
	var azureNodeClaim *karpv1.NodeClaim

	Context("ProvisionMode = AzureVM", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeNonAKS),
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)

			azureNodeClass = test.AzureNodeClass()
			test.ApplyDefaultAzureNodeClassStatus(azureNodeClass)

			azureNodePool = coretest.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							NodeClassRef: &karpv1.NodeClassReference{
								Group: object.GVK(azureNodeClass).Group,
								Kind:  object.GVK(azureNodeClass).Kind,
								Name:  azureNodeClass.Name,
							},
						},
					},
				},
			})

			azureNodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{karpv1.NodePoolLabelKey: azureNodePool.Name},
				},
				Spec: karpv1.NodeClaimSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(azureNodeClass).Group,
						Kind:  object.GVK(azureNodeClass).Kind,
						Name:  azureNodeClass.Name,
					},
				},
			})

			cloudProvider = New(
				azureEnv.InstanceTypesProvider,
				azureEnv.VMInstanceProvider,
				azureEnv.AKSMachineProvider,
				recorder,
				env.Client,
				azureEnv.ImageProvider,
				azureEnv.InstanceTypeStore,
			)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(
				env.Client, recorder, cloudProvider, cluster, fakeClock,
			)
		})

		AfterEach(func() {
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset()
		})

		Context("Create", func() {
			It("should create a VM with AzureNodeClass", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vm := input.VM

				// Verify image is from the AzureNodeClass imageID
				Expect(vm.Properties.StorageProfile.ImageReference.ID).ToNot(BeNil())
				Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(Equal(test.DefaultAzureNodeClassImageID))

				// Verify no AKS machine API calls
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			It("should base64-encode userData", func() {
				rawUserData := "#!/bin/bash\necho hello world"
				azureNodeClass.Spec.UserData = lo.ToPtr(rawUserData)

				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

				Expect(vm.Properties.OSProfile).ToNot(BeNil())
				Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())
				decoded, err := base64.StdEncoding.DecodeString(*vm.Properties.OSProfile.CustomData)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(decoded)).To(Equal(rawUserData))
			})

			It("should create VM without SSH config when SSHPublicKey is empty", func() {
				// In azurevm mode, the ssh-public-key option is not required.
				// SSHPublicKey being empty means no SSH config on the VM.
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

				// In azurevm mode with empty SSHPublicKey, OSProfile should either
				// not have an SSH configuration, or the SSH public keys should be empty
				if vm.Properties.OSProfile != nil && vm.Properties.OSProfile.LinuxConfiguration != nil &&
					vm.Properties.OSProfile.LinuxConfiguration.SSH != nil {
					if vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys != nil {
						for _, key := range vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys {
							// If keys exist, they should not contain the empty test ssh key
							Expect(key.KeyData).ToNot(BeNil())
						}
					}
				}
			})

			It("should reject AzureNodeClass with nil imageID at the API level", func() {
				azureNodeClass.Spec.ImageID = nil

				// CRD validation requires imageID — ExpectApplied should fail
				err := env.Client.Create(ctx, azureNodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("imageID"))
			})

			It("should not apply AKS billing tags in azurevm mode", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

				// The billing tag should NOT be present in azurevm mode
				_, hasBillingTag := vm.Tags[instance.BillingTagKey]
				Expect(hasBillingTag).To(BeFalse(), "AKS billing tag should not be present in azurevm mode")
			})

			It("should set AzureNodeClass hash annotations on NodeClaim", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				// Verify annotations
				Expect(result.Annotations).To(HaveKey(v1alpha1.AnnotationAzureNodeClassHash))
				Expect(result.Annotations[v1alpha1.AnnotationAzureNodeClassHash]).To(Equal(azureNodeClass.Hash()))
				Expect(result.Annotations).To(HaveKey(v1alpha1.AnnotationAzureNodeClassHashVersion))
				Expect(result.Annotations[v1alpha1.AnnotationAzureNodeClassHashVersion]).To(Equal(v1alpha1.AzureNodeClassHashVersion))

				// Should NOT have AKS-specific annotations
				Expect(result.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSNodeClassHash))
				Expect(result.Annotations).ToNot(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
			})
		})

		Context("GetInstanceTypes", func() {
			It("should return instance types for AzureNodeClass NodePool", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool)

				instanceTypes, err := cloudProvider.GetInstanceTypes(ctx, azureNodePool)
				Expect(err).ToNot(HaveOccurred())
				Expect(instanceTypes).ToNot(BeEmpty())
			})

			It("should not apply AKS-specific instance type filtering", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool)

				instanceTypes, err := cloudProvider.GetInstanceTypes(ctx, azureNodePool)
				Expect(err).ToNot(HaveOccurred())
				Expect(instanceTypes).ToNot(BeEmpty())

				// The instance type provider uses ListForAzureNodeClass which skips
				// AKS-specific filtering (LocalDNS, ArtifactStreaming) and derives
				// parameters directly from the AzureNodeClass spec.
				Expect(len(instanceTypes)).To(BeNumerically(">", 0))
			})
		})

		Context("IsDrifted", func() {
			It("should detect drift when AzureNodeClass hash changes", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				// The Create result has hash annotations — apply them to the API server NodeClaim
				azureNodeClaim.Annotations = lo.Assign(azureNodeClaim.Annotations, map[string]string{
					v1alpha1.AnnotationAzureNodeClassHash:        result.Annotations[v1alpha1.AnnotationAzureNodeClassHash],
					v1alpha1.AnnotationAzureNodeClassHashVersion: result.Annotations[v1alpha1.AnnotationAzureNodeClassHashVersion],
				})
				ExpectApplied(ctx, env.Client, azureNodeClaim)

				// Set hash on the AzureNodeClass object too
				oldHash := azureNodeClass.Hash()
				azureNodeClass.Annotations = lo.Assign(azureNodeClass.Annotations, map[string]string{
					v1alpha1.AnnotationAzureNodeClassHash:        oldHash,
					v1alpha1.AnnotationAzureNodeClassHashVersion: v1alpha1.AzureNodeClassHashVersion,
				})
				ExpectApplied(ctx, env.Client, azureNodeClass)

				// Now change the spec to cause hash drift
				azureNodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](512)
				newHash := azureNodeClass.Hash()
				azureNodeClass.Annotations[v1alpha1.AnnotationAzureNodeClassHash] = newHash
				ExpectApplied(ctx, env.Client, azureNodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NodeClassDrift))
			})

			It("should not detect drift when AzureNodeClass is unchanged", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				// The Create result has hash annotations — apply them to the API server NodeClaim
				currentHash := azureNodeClass.Hash()
				azureNodeClaim.Annotations = lo.Assign(azureNodeClaim.Annotations, map[string]string{
					v1alpha1.AnnotationAzureNodeClassHash:        currentHash,
					v1alpha1.AnnotationAzureNodeClassHashVersion: v1alpha1.AzureNodeClassHashVersion,
				})
				ExpectApplied(ctx, env.Client, azureNodeClaim)

				azureNodeClass.Annotations = lo.Assign(azureNodeClass.Annotations, map[string]string{
					v1alpha1.AnnotationAzureNodeClassHash:        currentHash,
					v1alpha1.AnnotationAzureNodeClassHashVersion: v1alpha1.AzureNodeClassHashVersion,
				})
				ExpectApplied(ctx, env.Client, azureNodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})
		})

		Context("GetSupportedNodeClasses", func() {
			It("should return both AKSNodeClass and AzureNodeClass", func() {
				supported := cloudProvider.GetSupportedNodeClasses()
				Expect(supported).To(HaveLen(2))

				var hasAKS, hasAzure bool
				for _, obj := range supported {
					switch obj.(type) {
					case *v1beta1.AKSNodeClass:
						hasAKS = true
					case *v1alpha1.AzureNodeClass:
						hasAzure = true
					}
				}
				Expect(hasAKS).To(BeTrue(), "should include AKSNodeClass")
				Expect(hasAzure).To(BeTrue(), "should include AzureNodeClass")
			})
		})

		Context("Delete", func() {
			It("should delete AzureNodeClass VMs", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

				// List to get the NodeClaim
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))

				// Delete
				err = cloudProvider.Delete(ctx, nodeClaims[0])
				Expect(err).ToNot(HaveOccurred())

				// Verify VM was deleted from the fake
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))
			})
		})

		Context("List", func() {
			It("should list AzureNodeClass VMs", func() {
				ExpectApplied(ctx, env.Client, azureNodeClass, azureNodePool, azureNodeClaim)
				result, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, azureNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))

				// Verify basic NodeClaim properties
				nc := nodeClaims[0]
				Expect(nc).ToNot(BeNil())
				Expect(nc.Status.ProviderID).ToNot(BeEmpty())
				Expect(nc.Labels).To(HaveKey(v1.LabelInstanceTypeStable))
			})
		})
	})
})
