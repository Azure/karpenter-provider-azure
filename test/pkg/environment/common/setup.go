// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package common

import (
	"fmt"
	"sync"
	"time"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,stylecheck
	. "github.com/onsi/gomega"    //nolint:revive,stylecheck
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/aws/karpenter-core/pkg/apis"
	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/operator/injection"
	"github.com/aws/karpenter-core/pkg/test"
	"github.com/aws/karpenter-core/pkg/utils/pod"
)

var (
	CleanableObjects = []client.Object{
		&v1.Pod{},
		&appsv1.Deployment{},
		&appsv1.DaemonSet{},
		&policyv1.PodDisruptionBudget{},
		&v1.PersistentVolumeClaim{},
		&v1.PersistentVolume{},
		&storagev1.StorageClass{},
		&v1alpha5.Provisioner{},
		&corev1beta1.NodePool{},
		&v1.LimitRange{},
		&schedulingv1.PriorityClass{},
		&v1.Node{},
		&v1alpha5.Machine{},
		&corev1beta1.NodeClaim{},
	}
)

// nolint:gocyclo
func (env *Environment) BeforeEach() {
	// TODO (charliedmcb): uncomment debugging lib, but removing references for now to avoid the need to review the entire package
	// debug.BeforeEach(env.Context, env.Config, env.Client)
	env.Context = injection.WithSettingsOrDie(env.Context, env.KubeClient, apis.Settings...)

	// Expect this cluster to be clean for test runs to execute successfully
	env.ExpectCleanCluster()

	env.Monitor.Reset()
	env.StartingNodeCount = env.Monitor.NodeCountAtReset()
}

func (env *Environment) ExpectCleanCluster() {
	var nodes v1.NodeList
	Expect(env.Client.List(env.Context, &nodes)).To(Succeed())
	for _, node := range nodes.Items {
		if len(node.Spec.Taints) == 0 && !node.Spec.Unschedulable {
			Fail(fmt.Sprintf("expected system pool node %s to be tainted", node.Name))
		}
	}
	var pods v1.PodList
	Expect(env.Client.List(env.Context, &pods)).To(Succeed())
	for i := range pods.Items {
		Expect(pods.Items[i].Namespace).ToNot(Equal("default"),
			fmt.Sprintf("expected no pods in the `default` namespace, found %s/%s", pods.Items[i].Namespace, pods.Items[i].Name))
	}
	Eventually(func(g Gomega) {
		var pods v1.PodList
		g.Expect(env.Client.List(env.Context, &pods)).To(Succeed())
		for i := range pods.Items {
			g.Expect(pod.IsProvisionable(&pods.Items[i])).To(BeFalse(),
				fmt.Sprintf("expected to have no provisionable pods, found %s/%s", pods.Items[i].Namespace, pods.Items[i].Name))
		}
	}).WithPolling(10 * time.Second).WithTimeout(5 * time.Minute).Should(Succeed())
	for _, obj := range []client.Object{&v1alpha5.Provisioner{}, &corev1beta1.NodePool{}, &v1alpha2.AKSNodeClass{}} {
		metaList := &metav1.PartialObjectMetadataList{}
		gvk := lo.Must(apiutil.GVKForObject(obj, env.Client.Scheme()))
		metaList.SetGroupVersionKind(gvk)
		Expect(env.Client.List(env.Context, metaList, client.Limit(1))).To(Succeed())
		Expect(metaList.Items).To(HaveLen(0), fmt.Sprintf("expected no %s to exist", gvk.Kind))
	}
}

func (env *Environment) Cleanup() {
	env.CleanupObjects(CleanableObjects...)
	env.eventuallyExpectScaleDown()
	env.ExpectNoCrashes()
}

func (env *Environment) AfterEach() {
	// TODO (charliedmcb): uncomment debugging lib, but removing references for now to avoid the need to review the entire package
	// debug.AfterEach(env.Context)
	env.printControllerLogs(&v1.PodLogOptions{Container: "controller"})
}

func (env *Environment) CleanupObjects(cleanableObjects ...client.Object) {
	time.Sleep(time.Second) // wait one second to let the caches get up-to-date for deletion
	wg := sync.WaitGroup{}
	for _, obj := range cleanableObjects {
		wg.Add(1)
		go func(obj client.Object) {
			defer wg.Done()
			defer GinkgoRecover()
			Eventually(func(g Gomega) {
				// This only gets the metadata for the objects since we don't need all the details of the objects
				metaList := &metav1.PartialObjectMetadataList{}
				metaList.SetGroupVersionKind(lo.Must(apiutil.GVKForObject(obj, env.Client.Scheme())))
				g.Expect(env.Client.List(env, metaList, client.HasLabels([]string{test.DiscoveryLabel}))).To(Succeed())
				// Limit the concurrency of these calls to 50 workers per object so that we try to limit how aggressively we
				// are deleting so that we avoid getting client-side throttled
				workqueue.ParallelizeUntil(env, 50, len(metaList.Items), func(i int) {
					defer GinkgoRecover()
					g.Expect(client.IgnoreNotFound(env.Client.Delete(env, &metaList.Items[i], client.PropagationPolicy(metav1.DeletePropagationForeground)))).To(Succeed())
				})
				// If the deletes eventually succeed, we should have no elements here at the end of the test
				g.Expect(env.Client.List(env, metaList, client.HasLabels([]string{test.DiscoveryLabel}), client.Limit(1))).To(Succeed())
				g.Expect(metaList.Items).To(HaveLen(0))
			}).Should(Succeed())
		}(obj)
	}
	wg.Wait()
}
