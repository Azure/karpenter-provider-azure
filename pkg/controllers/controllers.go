// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controllers

import (
	"context"

	"github.com/aws/karpenter-core/pkg/operator/controller"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter/pkg/cloudprovider"
	nodeclaimgarbagecollection "github.com/Azure/karpenter/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/Azure/karpenter/pkg/controllers/nodeclaim/inplaceupdate"
	nodeclaimlink "github.com/Azure/karpenter/pkg/controllers/nodeclaim/link"
	"github.com/Azure/karpenter/pkg/providers/instance"
	"github.com/Azure/karpenter/pkg/utils/project"
)

func NewControllers(ctx context.Context, kubeClient client.Client, cloudProvider *cloudprovider.CloudProvider, instanceProvider *instance.Provider) []controller.Controller {
	logging.FromContext(ctx).With("version", project.Version).Debugf("discovered version")
	linkController := nodeclaimlink.NewController(kubeClient, cloudProvider)
	controllers := []controller.Controller{
		nodeclaimgarbagecollection.NewController(kubeClient, cloudProvider, linkController),
		linkController,
		inplaceupdate.NewController(kubeClient, instanceProvider),
	}
	return controllers
}
