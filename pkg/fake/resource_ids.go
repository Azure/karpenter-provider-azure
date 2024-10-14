package fake

import (
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/google/uuid"
)

// SubnetID will generate a fake subnet ID that belongs to the same VNET as the main VnetSubnetID set on options
func SubnetID(options *options.Options) string {
	clusterVNETComponents, _ := utils.GetVnetSubnetIDComponents(options.SubnetID)
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s",
		clusterVNETComponents.SubscriptionID,
		clusterVNETComponents.ResourceGroupName,
		clusterVNETComponents.VNetName,
		uuid.New().String())
}
