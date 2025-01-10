package garbagecollection

import (
	"time"
	"context"
	"fmt"


	"github.com/samber/lo"
	"github.com/patrickmn/go-cache"
	"knative.dev/pkg/logging"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"github.com/awslabs/operatorpkg/singleton"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"


	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
)
 

const (
	NICGCControllerName = "networkinterface.garbagecollection"
	NicReservationDuration = time.Second * 180
)



type NetworkInterfaceController struct {
	kubeClient      client.Client
	instanceProvider instance.Provider
	// A networkInterface is considered unremovable if it meets the following 3 criteria  
	// 1: Reserved by NRP: When creating a nic and attempting to assign it to a vm, the nic will be reserved for that vm arm_resource_id for 180 seconds
	// 2: Belongs to a Nodeclaim: If a nodeclaim is 
	// 3: Belongs to VM: If the VM Garbage Collection controller is removing a vm, we should not attempt removing it in this controller
	unremovableNics *cache.Cache 

}

func NewNetworkInterfaceController(kubeClient client.Client,  instanceProvider instance.Provider, unremovableNics *cache.Cache) *NetworkInterfaceController {
	return &NetworkInterfaceController{
		kubeClient:      kubeClient,
		instanceProvider: instanceProvider,
		unremovableNics: unremovableNics,
	}
}

func (c *NetworkInterfaceController) Reconcile(ctx context.Context) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, NICGCControllerName)
	
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing NodeClaims for NIC GC: %w", err)
	}
	
	
	// List all NICs from the instance provider, this List call will give us network interfaces that belong to karpenter
	nics, err := c.instanceProvider.ListNics(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing NICs: %w", err)
	}
	
	// resourceNames is the resource representation for each nodeclaim
	resourceNames := sets.New[string]() 
	for _, nodeClaim := range nodeClaimList.Items {
		// Adjust the prefix as per the aks naming convention
		resourceNames.Insert(fmt.Sprintf("aks-%s", nodeClaim.Name))
	}

	errs := make([]error, len(nics))
	workqueue.ParallelizeUntil(ctx, 100, len(nics), func(i int){
		nicName := lo.FromPtr(nics[i].Name)
		_, removableNic := c.unremovableNics.Get(nicName)
		noNodeclaimExistsForNIC := !resourceNames.Has(nicName)
		// The networkInterface is unremovable if its  
		// A: Reserved by NRP  
		// B: Belongs to a Nodeclaim
		// C: Belongs to VM
		if noNodeclaimExistsForNIC && removableNic {
			err := c.instanceProvider.DeleteNic(ctx, nicName) 
			if sdkerrors.IsNicReservedForAnotherVM(err) { 
				c.unremovableNics.Set(nicName, sdkerrors.NicReservedForAnotherVM, NicReservationDuration)
					return	
			}
			if err != nil { 
				errs[i] = err 
				return 
			}

			logging.FromContext(ctx).With("nic", nicName).Infof("garbage collected NIC")
		}
	})

	// requeue every 5 minutes, adjust for throttling?
	return reconcile.Result{
		Requeue: true, 
		RequeueAfter: time.Minute * 5,
	}, nil
}

func (c *NetworkInterfaceController) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m). 
		Named(NICGCControllerName). 
		WatchesRawSource(singleton.Source()). 
		Complete(singleton.AsReconciler(c))
}
