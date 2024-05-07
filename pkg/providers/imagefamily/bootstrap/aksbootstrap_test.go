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

package bootstrap_test

import (
	"fmt"

	nbcontractv1 "github.com/Azure/agentbaker/pkg/proto/nbcontract/v1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	core "k8s.io/api/core/v1"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
)

var _ = Describe("Aksbootstrap", func() {

	DescribeTable("Testing KubeBinary Url",
		func(version, kubeBinaryURL string) {
			b := &bootstrap.AKS{}
			Expect(bootstrap.ExportKubeBinaryURL(b, version, "amd64")).To(Equal(kubeBinaryURL))
		},
		Entry("with Kubernetes version 1.24.5", "1.24.5", fmt.Sprintf("%s/kubernetes/v1.24.5/binaries/kubernetes-node-linux-amd64.tar.gz", bootstrap.ExportGlobalAKSMirror)),
		Entry("with Kubernetes version 1.25.2", "1.25.2", fmt.Sprintf("%s/kubernetes/v1.25.2/binaries/kubernetes-node-linux-amd64.tar.gz", bootstrap.ExportGlobalAKSMirror)),
		Entry("with Kubernetes version 1.26.0", "1.26.0", fmt.Sprintf("%s/kubernetes/v1.26.0/binaries/kubernetes-node-linux-amd64.tar.gz", bootstrap.ExportGlobalAKSMirror)),
		Entry("with Kubernetes version 1.27.1", "1.27.1", fmt.Sprintf("%s/kubernetes/v1.27.1/binaries/kubernetes-node-linux-amd64.tar.gz", bootstrap.ExportGlobalAKSMirror)),
	)

	DescribeTable("aks BootstrapScript", func(validator func(*bootstrap.AKS)) {
		a := &bootstrap.AKS{
			Options: bootstrap.Options{
				ClusterName:     "clustername",
				ClusterEndpoint: "clusterendpoint",
				KubeletConfig:   &corev1beta1.KubeletConfiguration{},
				Taints:          []core.Taint{},
				Labels:          map[string]string{},
				CABundle:        lo.ToPtr("cabundle"),
				VMSize:          "vmsize",
				SubnetID:        "/SubscriPtioNS/SUB_ID/REsourceGroupS/RG_NAME/ProViderS/MicrosoFT.NetWorK/VirtualNetWorKS/VNET_NAME/SubneTS/SUBNET_NAME",
			},
			Arch:                           "amd64",
			TenantID:                       "tenantid",
			SubscriptionID:                 "subscriptionid",
			UserAssignedIdentityID:         "userassignedidentityid",
			Location:                       "location",
			ResourceGroup:                  "resourcegroup",
			ClusterID:                      "clusterid",
			APIServerName:                  "apiservername",
			KubeletClientTLSBootstrapToken: "kubeletclienttlsbootstraptoken",
			NetworkPlugin:                  "networkplugin",
			NetworkPolicy:                  "networkpolicy",
			KubernetesVersion:              "1.24.5",
		}
		if validator != nil {
			validator(a)
		}
	},
		Entry("with all required fields should expect no error and non-empty script",
			func(a *bootstrap.AKS) {
				script, err := bootstrap.ExportAKSBootstrapScript(a)
				Expect(err).To(BeNil())
				Expect(script).ToNot(BeEmpty())
			},
		),
		Entry("with missing required field (ResourceGroup) should expect error",
			func(a *bootstrap.AKS) {
				a.ResourceGroup = ""
				script, err := bootstrap.ExportAKSBootstrapScript(a)
				Expect(err).ToNot(BeNil())
				Expect(script).To(BeEmpty())
			},
		),
	)

	DescribeTable("aks ApplyOptions", func(validator func(*bootstrap.AKS)) {
		a := &bootstrap.AKS{
			Options: bootstrap.Options{
				ClusterName:     "clustername",
				ClusterEndpoint: "clusterendpoint",
				KubeletConfig:   &corev1beta1.KubeletConfiguration{},
				Taints:          []core.Taint{},
				Labels:          map[string]string{},
				CABundle:        lo.ToPtr("cabundle"),
				VMSize:          "vmsize",
				SubnetID:        "/SubscriPtioNS/SUB_ID/REsourceGroupS/RG_NAME/ProViderS/MicrosoFT.NetWorK/VirtualNetWorKS/VNET_NAME/SubneTS/SUBNET_NAME",
			},
			Location:       "Updated location",
			ResourceGroup:  "Updated resourcegroup",
			SubscriptionID: "AKS subscriptionid",
			TenantID:       "AKS tenantid",
			APIServerName:  "AKS apiservername",
		}
		if validator != nil {
			validator(a)
		}
	},
		Entry("with all required fields should expect no error and non-empty script",
			func(a *bootstrap.AKS) {
				nbconfig, err := bootstrap.ExportAKSApplyOptions(a, &nbcontractv1.Configuration{
					ClusterConfig: &nbcontractv1.ClusterConfig{
						Location:      "AKS location",
						ResourceGroup: "AKS resourcegroup",
					},
					AuthConfig: &nbcontractv1.AuthConfig{
						SubscriptionId: "AKS subscriptionid",
						TenantId:       "AKS tenantid",
					},
					ApiServerConfig: &nbcontractv1.ApiServerConfig{
						ApiServerName: "AKS apiservername",
					},
				})
				Expect(err).To(BeNil())
				Expect(nbconfig.ClusterConfig.Location).To(Equal("Updated location"))
				Expect(nbconfig.ClusterConfig.ResourceGroup).To(Equal("Updated resourcegroup"))
				Expect(nbconfig.AuthConfig.SubscriptionId).To(Equal("AKS subscriptionid"))
				Expect(nbconfig.AuthConfig.TenantId).To(Equal("AKS tenantid"))
				Expect(nbconfig.ApiServerConfig.ApiServerName).To(Equal("AKS apiservername"))
			},
		),
		Entry("with missing required field (ResourceGroup) should expect error",
			func(a *bootstrap.AKS) {
				a.ResourceGroup = ""
				nbconfig, err := bootstrap.ExportAKSApplyOptions(a, &nbcontractv1.Configuration{
					ClusterConfig: &nbcontractv1.ClusterConfig{
						Location:      "AKS location",
						ResourceGroup: "AKS resourcegroup",
					},
				})
				Expect(err).To(MatchError("error when validating node bootstrap contract: required field ClusterConfig.ResourceGroup is missing"))
				Expect(nbconfig).To(BeNil())
			},
		),
	)

})
