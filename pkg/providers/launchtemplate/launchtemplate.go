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

package launchtemplate

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	karplabels "github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
type Template struct {
	ScriptlessCustomData      string
	ImageID                   string
	SubnetID                  string
	Tags                      map[string]*string
	CustomScriptsCustomData   string
	CustomScriptsCSE          string
	IsWindows                 bool
	StorageProfileDiskType    string
	StorageProfileIsEphemeral bool
	StorageProfilePlacement   armcompute.DiffDiskPlacement
	StorageProfileSizeGB      int32
	// OpenShiftUserData is the raw userData (Ignition config) for OpenShift mode.
	// When set, this bypasses all AKS-specific bootstrap logic.
	OpenShiftUserData string
}

type Provider struct {
	imageFamily             imagefamily.Resolver
	imageProvider           imagefamily.NodeImageProvider
	kubeClient              client.Client
	caBundle                *string
	clusterEndpoint         string
	tenantID                string
	subscriptionID          string
	kubeletIdentityClientID string
	resourceGroup           string
	clusterResourceGroup    string
	location                string
	vnetGUID                string
	provisionMode           string
}

// TODO: add caching of launch templates

// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
func NewProvider(_ context.Context, imageFamily imagefamily.Resolver, imageProvider imagefamily.NodeImageProvider, kubeClient client.Client, caBundle *string, clusterEndpoint string,
	tenantID, subscriptionID, clusterResourceGroup string, kubeletIdentityClientID, resourceGroup, location, vnetGUID, provisionMode string,
) *Provider {
	return &Provider{
		imageFamily:             imageFamily,
		imageProvider:           imageProvider,
		kubeClient:              kubeClient,
		caBundle:                caBundle,
		clusterEndpoint:         clusterEndpoint,
		tenantID:                tenantID,
		subscriptionID:          subscriptionID,
		kubeletIdentityClientID: kubeletIdentityClientID,
		resourceGroup:           resourceGroup,
		clusterResourceGroup:    clusterResourceGroup,
		location:                location,
		vnetGUID:                vnetGUID,
		provisionMode:           provisionMode,
	}
}

// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
func (p *Provider) GetTemplate(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *cloudprovider.InstanceType,
	additionalLabels map[string]string,
) (*Template, error) {
	// For OpenShift mode, we can skip much of the AKS-specific template generation
	// TODO(macao): I think we are skipping too much here, but we can come back to this later.
	if p.provisionMode == consts.ProvisionModeOpenShift {
		return p.getOpenShiftTemplate(ctx, nodeClass)
	}

	staticParameters, err := p.getStaticParameters(ctx, instanceType, nodeClass, lo.Assign(nodeClaim.Labels, additionalLabels))
	if err != nil {
		return nil, err
	}

	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		// Note: we check GetKubernetesVersion for errors at the start of the Create call, so this case should not happen.
		return nil, err
	}
	staticParameters.KubernetesVersion = kubernetesVersion
	templateParameters, err := p.imageFamily.Resolve(ctx, nodeClass, nodeClaim, instanceType, staticParameters)
	if err != nil {
		return nil, err
	}
	launchTemplate, err := p.createLaunchTemplate(ctx, templateParameters)
	if err != nil {
		return nil, err
	}

	launchTemplate.Tags = Tags(options.FromContext(ctx), nodeClass, nodeClaim)

	return launchTemplate, nil
}

// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
func (p *Provider) getStaticParameters(
	ctx context.Context,
	instanceType *cloudprovider.InstanceType,
	nodeClass *v1beta1.AKSNodeClass,
	labels map[string]string,
) (*parameters.StaticParameters, error) {
	var arch string = karpv1.ArchitectureAmd64
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64))); err == nil {
		arch = karpv1.ArchitectureArm64
	}

	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)
	baseLabels, err := karplabels.Get(ctx, nodeClass)
	if err != nil {
		return nil, err
	}
	labels = lo.Assign(baseLabels, labels)

	// ATTENTION!!!: changes here will NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
	// Refactoring for code unification is not being invested immediately.
	return &parameters.StaticParameters{
		ClusterName:                    options.FromContext(ctx).ClusterName,
		ClusterEndpoint:                p.clusterEndpoint,
		Labels:                         labels,
		CABundle:                       p.caBundle,
		Arch:                           arch,
		GPUNode:                        utils.IsNvidiaEnabledSKU(instanceType.Name),
		GPUDriverVersion:               utils.GetGPUDriverVersion(instanceType.Name),
		GPUDriverType:                  utils.GetGPUDriverType(instanceType.Name),
		GPUImageSHA:                    utils.GetAKSGPUImageSHA(instanceType.Name),
		TenantID:                       p.tenantID,
		SubscriptionID:                 p.subscriptionID,
		KubeletIdentityClientID:        p.kubeletIdentityClientID,
		ResourceGroup:                  p.resourceGroup,
		Location:                       p.location,
		ClusterID:                      options.FromContext(ctx).ClusterID,
		APIServerName:                  options.FromContext(ctx).GetAPIServerName(),
		KubeletClientTLSBootstrapToken: options.FromContext(ctx).KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  getAgentbakerNetworkPlugin(ctx),
		NetworkPolicy:                  options.FromContext(ctx).NetworkPolicy,
		SubnetID:                       subnetID,
		ClusterResourceGroup:           p.clusterResourceGroup,
	}, nil
}

func getAgentbakerNetworkPlugin(ctx context.Context) string {
	opts := options.FromContext(ctx)
	if opts.IsAzureCNIOverlay() || opts.IsCiliumNodeSubnet() || opts.IsNetworkPluginNone() {
		return consts.NetworkPluginNone
	}
	return consts.NetworkPluginAzure
}

// ATTENTION!!!: changes here may NOT be effective on AKS machine nodes (ProvisionModeAKSMachineAPI); See aksmachineinstance.go/aksmachineinstancehelpers.go.
// Refactoring for code unification is not being invested immediately.
// getOpenShiftTemplate creates a simplified template for OpenShift mode.
// It uses userData and imageID directly from the nodeClass, bypassing AKS-specific bootstrap.
func (p *Provider) getOpenShiftTemplate(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (*Template, error) {
	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), options.FromContext(ctx).SubnetID)

	template := &Template{
		SubnetID:               subnetID,
		StorageProfileDiskType: "Premium_LRS",
		StorageProfileSizeGB:   lo.FromPtrOr(nodeClass.Spec.OSDiskSizeGB, 128),
	}

	// Set ImageID from nodeClass
	if nodeClass.Spec.ImageID != nil && *nodeClass.Spec.ImageID != "" {
		template.ImageID = *nodeClass.Spec.ImageID
	}

	// Set UserData - prefer secret ref over inline
	if nodeClass.HasUserDataSecretRef() {
		userData, err := p.getUserDataFromSecret(ctx, nodeClass)
		fmt.Printf("OPENSHIFT USERDATA FROM SECRET: %s", userData)
		if err != nil {
			return nil, fmt.Errorf("fetching userData from secret: %w", err)
		}
		template.OpenShiftUserData = userData
	} else if nodeClass.Spec.UserData != nil && *nodeClass.Spec.UserData != "" {
		template.OpenShiftUserData = *nodeClass.Spec.UserData
	}

	return template, nil
}

// getUserDataFromSecret fetches the userData from a Kubernetes secret and returns it base64-encoded
func (p *Provider) getUserDataFromSecret(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (string, error) {
	secret := &v1.Secret{}
	secretName := nodeClass.Spec.UserDataSecretRef.Name
	secretNamespace := nodeClass.GetUserDataSecretNamespace()

	if err := p.kubeClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, secret); err != nil {
		return "", fmt.Errorf("getting secret %s/%s: %w", secretNamespace, secretName, err)
	}

	userData, ok := secret.Data["userData"]
	if !ok {
		return "", fmt.Errorf("secret %s/%s does not have 'userData' key", secretNamespace, secretName)
	}

	// Azure CustomData must be base64-encoded
	return base64.StdEncoding.EncodeToString(userData), nil
}

func (p *Provider) createLaunchTemplate(ctx context.Context, params *parameters.Parameters) (*Template, error) {
	template := &Template{
		ImageID:                   params.ImageID,
		SubnetID:                  params.SubnetID,
		IsWindows:                 params.IsWindows,
		StorageProfileDiskType:    params.StorageProfileDiskType,
		StorageProfileIsEphemeral: params.StorageProfileIsEphemeral,
		StorageProfilePlacement:   params.StorageProfilePlacement,
		StorageProfileSizeGB:      params.StorageProfileSizeGB,
	}

	switch p.provisionMode {
	case consts.ProvisionModeOpenShift:
		// OpenShift mode: userData is passed directly via the NodeClass, no generation needed.
		// The OpenShiftUserData field will be set separately by the caller.
		// This mode bypasses all AKS-specific bootstrap logic.
		return template, nil
	case consts.ProvisionModeBootstrappingClient:
		customData, cse, err := params.CustomScriptsNodeBootstrapping.GetCustomDataAndCSE(ctx)
		if err != nil {
			return nil, err
		}
		template.CustomScriptsCustomData = customData
		template.CustomScriptsCSE = cse
	case consts.ProvisionModeAKSScriptless:
		// render user data
	default:
		// aksscriptless mode (default)
		userData, err := params.ScriptlessCustomData.Script()
		if err != nil {
			return nil, err
		}
		template.ScriptlessCustomData = userData
	}

	return template, nil
}
