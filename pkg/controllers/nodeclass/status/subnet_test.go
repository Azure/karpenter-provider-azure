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

package status_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	opstatus "github.com/awslabs/operatorpkg/status"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

var _ = Describe("SubnetStatus", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("should mark nodeclass as ready when subnet exists and has capacity", func() {
		azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
			return armnetwork.SubnetsClientGetResponse{
				Subnet: armnetwork.Subnet{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: lo.ToPtr("10.0.0.0/16"),
						IPConfigurations: []*armnetwork.IPConfiguration{
							{}, {}, {}, {}, {}, // 5 used IPs
						},
					},
				},
			}, nil
		}

		ExpectApplied(ctx, env.Client, nodeClass)
		ExpectObjectReconciled(ctx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(ctx, env.Client, nodeClass)

		cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
		Expect(cond.IsTrue()).To(BeTrue())

		readyCondition := nodeClass.StatusConditions().Get(opstatus.ConditionReady)
		Expect(readyCondition.IsTrue()).To(BeTrue())
	})

	It("should use nodeclass subnet ID when specified (BYO VNet)", func() {
		// Override context to use a BYO VNet instead of managed VNet
		byoOpts := test.Options(test.OptionsFields{
			SubnetID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/cluster-subnet"),
		})
		byoCtx := options.ToContext(ctx, byoOpts)

		nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/nodeclass-subnet")

		azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
			Expect(resourceGroupName).To(Equal("test-resourceGroup"))
			Expect(virtualNetworkName).To(Equal("byo-vnet-customname"))
			Expect(subnetName).To(Equal("nodeclass-subnet"))

			return armnetwork.SubnetsClientGetResponse{
				Subnet: armnetwork.Subnet{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: lo.ToPtr("10.0.0.0/16"),
					},
				},
			}, nil
		}

		ExpectApplied(byoCtx, env.Client, nodeClass)
		ExpectObjectReconciled(byoCtx, env.Client, controller, nodeClass)
		nodeClass = ExpectExists(byoCtx, env.Client, nodeClass)

		cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
		Expect(cond.IsTrue()).To(BeTrue())

		readyCondition := nodeClass.StatusConditions().Get(opstatus.ConditionReady)
		Expect(readyCondition.IsTrue()).To(BeTrue())
	})

	Context("SubnetReconciler direct tests", func() {
		var reconciler *status.SubnetReconciler

		BeforeEach(func() {
			reconciler = status.NewSubnetReconciler(azureEnv.SubnetsAPI)
			nodeClass = test.AKSNodeClass()
		})

		It("should mark nodeclass as ready when subnet exists with sufficient capacity", func() {
			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{
							AddressPrefix: lo.ToPtr("10.0.0.0/16"),
							IPConfigurations: []*armnetwork.IPConfiguration{
								{}, {}, {}, {}, {}, // 5 used IPs
							},
						},
					},
				}, nil
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute * 3}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsTrue()).To(BeTrue())
		})

		It("should mark nodeclass as not ready when subnet doesn't exist", func() {
			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{}, &azcore.ResponseError{
					ErrorCode:  "ResourceNotFound",
					StatusCode: http.StatusNotFound,
					RawResponse: &http.Response{
						StatusCode: http.StatusNotFound,
					},
				}
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal("SubnetNotFound"))
		})

		It("should use nodeclass subnet ID when specified (BYO VNet)", func() {
			// Override context to use a BYO VNet instead of managed VNet
			byoOpts := test.Options(test.OptionsFields{
				SubnetID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/cluster-subnet"),
			})
			byoCtx := options.ToContext(ctx, byoOpts)

			nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/nodeclass-subnet")

			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				Expect(resourceGroupName).To(Equal("test-resourceGroup"))
				Expect(virtualNetworkName).To(Equal("byo-vnet-customname"))
				Expect(subnetName).To(Equal("nodeclass-subnet"))

				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{
							AddressPrefix: lo.ToPtr("10.0.0.0/16"),
						},
					},
				}, nil
			}

			result, err := reconciler.Reconcile(byoCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute * 3}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsTrue()).To(BeTrue())
		})

		It("should mark nodeclass as not ready when custom subnet is in managed VNet", func() {
			nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/custom-subnet")

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal("SubnetIDInvalid"))
			Expect(cond.Message).To(ContainSubstring("custom subnet cannot be in the same VNet as cluster managed VNet"))
		})

		It("should preserve the transition time when the subnet remains invalid", func() {
			nodeClass.Spec.VNETSubnetID = lo.ToPtr("invalid")

			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			transitionTime := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady).LastTransitionTime
			time.Sleep(time.Millisecond)

			_, err = reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady).LastTransitionTime).To(Equal(transitionTime))
		})

		It("should mark nodeclass as not ready when subnet hits unknown error", func() {
			const errString = "An unexpected internal server error occurred while processing the request. The service encountered an unrecoverable condition and was unable to complete the operation. Please retry the request after some time. If the problem persists, contact Azure support with the correlation ID and timestamp for further investigation."
			azureEnv.SubnetsAPI.GetFunc = func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{}, &azcore.ResponseError{
					ErrorCode:  "InternalServerError",
					StatusCode: http.StatusInternalServerError,
					RawResponse: &http.Response{
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"code":"InternalServerError","message":"%s"}}`, errString))),
					},
				}
			}

			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal("SubnetUnknownError"))
			Expect(cond.Message).To(ContainSubstring(errString))
		})
	})

	Context("PodSubnetID", func() {
		const (
			nodeSubnetID     = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet/subnets/nodesubnet"
			podSubnetID      = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet/subnets/podsubnet"
			otherVNetSubnet  = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/other-vnet/subnets/podsubnet"
			clusterPodSubnet = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet/subnets/clusterpodsubnet"
		)

		var (
			reconciler   *status.SubnetReconciler
			podSubnetCtx context.Context
		)

		BeforeEach(func() {
			reconciler = status.NewSubnetReconciler(azureEnv.SubnetsAPI)
			nodeClass = test.AKSNodeClass()
			nodeClass.Spec.VNETSubnetID = lo.ToPtr(nodeSubnetID)
			podSubnetCtx = options.ToContext(ctx, test.Options(test.OptionsFields{
				SubnetID:          lo.ToPtr(nodeSubnetID),
				PodSubnetID:       lo.ToPtr(clusterPodSubnet),
				NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeNone),
			}))
			azureEnv.SubnetsAPI.GetFunc = func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: lo.ToPtr("10.0.0.0/16")},
					},
				}, nil
			}
		})

		It("should mark nodeclass as ready and look up the pod subnet when podSubnetID is valid", func() {
			nodeClass.Spec.PodSubnetID = lo.ToPtr(podSubnetID)
			lookedUp := sets.New[string]()
			azureEnv.SubnetsAPI.GetFunc = func(_ context.Context, _ string, virtualNetworkName string, subnetName string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				Expect(virtualNetworkName).To(Equal("byo-vnet"))
				lookedUp.Insert(subnetName)
				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: lo.ToPtr("10.0.0.0/16")},
					},
				}, nil
			}

			_, err := reconciler.Reconcile(podSubnetCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			// Both the pod subnet and the node subnet must be validated, not just the node subnet
			Expect(lookedUp.UnsortedList()).To(ConsistOf("podsubnet", "nodesubnet"))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady).IsTrue()).To(BeTrue())
		})

		It("should mark nodeclass as not ready when podSubnetID is in a different VNet than the node subnet", func() {
			nodeClass.Spec.PodSubnetID = lo.ToPtr(otherVNetSubnet)

			result, err := reconciler.Reconcile(podSubnetCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal("SubnetIDInvalid"))
			Expect(cond.Message).To(ContainSubstring("same virtual network as the node subnet"))
		})

		It("should mark nodeclass as not ready when podSubnetID matches the node subnet", func() {
			nodeClass.Spec.PodSubnetID = lo.ToPtr(nodeSubnetID)

			result, err := reconciler.Reconcile(podSubnetCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Message).To(ContainSubstring("must be different from the node subnet"))
		})

		It("should mark nodeclass as not ready when podSubnetID is set with overlay", func() {
			nodeClass.Spec.PodSubnetID = lo.ToPtr(podSubnetID)
			overlayCtx := options.ToContext(ctx, test.Options(test.OptionsFields{
				SubnetID:          lo.ToPtr(nodeSubnetID),
				PodSubnetID:       lo.ToPtr(clusterPodSubnet),
				NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeOverlay),
			}))

			result, err := reconciler.Reconcile(overlayCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Message).To(ContainSubstring("only supported with Azure CNI without overlay"))
		})

		// AKS machine nodes never get the pod network labels, so maxPods must not jump to the pod subnet default
		It("should mark nodeclass as not ready when podSubnetID is set in an AKS machine API mode", func() {
			nodeClass.Spec.PodSubnetID = lo.ToPtr(podSubnetID)
			machineAPICtx := options.ToContext(ctx, test.Options(test.OptionsFields{
				SubnetID:          lo.ToPtr(nodeSubnetID),
				PodSubnetID:       lo.ToPtr(clusterPodSubnet),
				NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeNone),
				ProvisionMode:     lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
			}))

			result, err := reconciler.Reconcile(machineAPICtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Message).To(ContainSubstring(consts.ProvisionModeAKSMachineAPI))
		})

		It("should mark nodeclass as not ready when podSubnetID is set without a cluster-level pod subnet", func() {
			nodeClass.Spec.PodSubnetID = lo.ToPtr(podSubnetID)
			noClusterDefaultCtx := options.ToContext(ctx, test.Options(test.OptionsFields{
				SubnetID:          lo.ToPtr(nodeSubnetID),
				NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeNone),
			}))

			result, err := reconciler.Reconcile(noClusterDefaultCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Message).To(ContainSubstring("requires the cluster-level --pod-subnet-id"))
		})

		It("should validate the inherited cluster pod subnet", func() {
			lookedUp := sets.New[string]()
			azureEnv.SubnetsAPI.GetFunc = func(_ context.Context, _ string, _ string, subnetName string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				lookedUp.Insert(subnetName)
				return armnetwork.SubnetsClientGetResponse{}, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					RawResponse: &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"NotFound","message":"pod subnet not found"}}`)),
					},
				}
			}

			result, err := reconciler.Reconcile(podSubnetCtx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))
			Expect(lookedUp.UnsortedList()).To(ConsistOf("clusterpodsubnet"))
			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal("SubnetNotFound"))
			Expect(cond.Message).To(ContainSubstring(clusterPodSubnet))
		})

		It("should not require a pod subnet lookup when the cluster has no pod subnet", func() {
			noPodSubnetCtx := options.ToContext(ctx, test.Options(test.OptionsFields{
				SubnetID:          lo.ToPtr(nodeSubnetID),
				NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeNone),
			}))
			lookedUp := sets.New[string]()
			azureEnv.SubnetsAPI.GetFunc = func(_ context.Context, _ string, _ string, subnetName string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
				lookedUp.Insert(subnetName)
				return armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: lo.ToPtr("10.0.0.0/16")},
					},
				}, nil
			}

			_, err := reconciler.Reconcile(noPodSubnetCtx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(lookedUp.UnsortedList()).To(ConsistOf("nodesubnet"))
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeSubnetsReady).IsTrue()).To(BeTrue())
		})
	})
})
