// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package debug

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corecontroller "github.com/aws/karpenter-core/pkg/operator/controller"
	"github.com/aws/karpenter-core/pkg/utils/pod"
)

type PodController struct {
	kubeClient client.Client
}

func NewPodController(kubeClient client.Client) *PodController {
	return &PodController{
		kubeClient: kubeClient,
	}
}

func (c *PodController) Name() string {
	return "pod"
}

func (c *PodController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	p := &v1.Pod{}
	if err := c.kubeClient.Get(ctx, req.NamespacedName, p); err != nil {
		if errors.IsNotFound(err) {
			fmt.Printf("[DELETED %s] POD %s\n", time.Now().Format(time.RFC3339), req.NamespacedName.String())
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	fmt.Printf("[CREATED/UPDATED %s] POD %s %s\n", time.Now().Format(time.RFC3339), req.NamespacedName.String(), c.GetInfo(p))
	return reconcile.Result{}, nil
}

func (c *PodController) GetInfo(p *v1.Pod) string {
	var containerInfo strings.Builder
	for _, c := range p.Status.ContainerStatuses {
		if containerInfo.Len() > 0 {
			_ = lo.Must(fmt.Fprintf(&containerInfo, ", "))
		}
		_ = lo.Must(fmt.Fprintf(&containerInfo, "%s restarts=%d", c.Name, c.RestartCount))
	}
	return fmt.Sprintf("provisionable=%v phase=%s nodename=%s owner=%#v [%s]",
		pod.IsProvisionable(p), p.Status.Phase, p.Spec.NodeName, p.OwnerReferences, containerInfo.String())
}

func (c *PodController) Builder(_ context.Context, m manager.Manager) corecontroller.Builder {
	return corecontroller.Adapt(controllerruntime.
		NewControllerManagedBy(m).
		For(&v1.Pod{}).
		WithEventFilter(predicate.And(
			predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldPod := e.ObjectOld.(*v1.Pod)
					newPod := e.ObjectNew.(*v1.Pod)
					return c.GetInfo(oldPod) != c.GetInfo(newPod)
				},
			},
			predicate.NewPredicateFuncs(func(o client.Object) bool {
				return o.GetNamespace() != "kube-system" && o.GetNamespace() != "karpenter"
			}),
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}))
}
