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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

var _ ImageFamily = (*Custom)(nil)

type Custom struct {
	Options *parameters.StaticParameters
}

func (c Custom) Name() string {
	return v1alpha2.CustomImageFamily
}

func (c Custom) DefaultImages() []DefaultImageOutput {
	return []DefaultImageOutput{
		{
			Distro: "Custom",
		},
	}
}

// UserData returns the default userdata script for the image Family
func (c Custom) ScriptlessCustomData(kubeletConfig *bootstrap.KubeletConfiguration, taints []v1.Taint, labels map[string]string, caBundle *string, _ *cloudprovider.InstanceType, apiUserData *string) bootstrap.Bootstrapper {
	return bootstrap.APIbootstrap{
		UserData: apiUserData,
	}
}

// UserData returns the default userdata script for the image Family
func (c Custom) CustomScriptsNodeBootstrapping(kubeletConfig *bootstrap.KubeletConfiguration, taints []v1.Taint, startupTaints []v1.Taint, labels map[string]string, instanceType *cloudprovider.InstanceType, imageDistro string, storageProfile string) customscriptsbootstrap.Bootstrapper {
	return customscriptsbootstrap.ProvisionClientBootstrap{}
}
