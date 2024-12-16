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

package common

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	loggingtesting "knative.dev/pkg/logging/testing"
	"knative.dev/pkg/system"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/operator"
)

type ContextKey string

const (
	GitRefContextKey = ContextKey("gitRef")
)

type Environment struct {
	context.Context
	cancel context.CancelFunc

	Client     client.Client
	Config     *rest.Config
	KubeClient kubernetes.Interface
	Monitor    *Monitor

	StartingNodeCount int
}

func NewEnvironment(t *testing.T) *Environment {
	ctx := loggingtesting.TestContextWithLogger(t)
	ctx, cancel := context.WithCancel(ctx)
	config := NewConfig()
	client := NewClient(ctx, config)

	lo.Must0(os.Setenv(system.NamespaceEnvKey, "karpenter"))
	if val, ok := os.LookupEnv("GIT_REF"); ok {
		ctx = context.WithValue(ctx, GitRefContextKey, val)
	}

	gomega.SetDefaultEventuallyTimeout(5 * time.Minute)
	gomega.SetDefaultEventuallyPollingInterval(1 * time.Second)
	return &Environment{
		Context:    ctx,
		cancel:     cancel,
		Config:     config,
		Client:     client,
		KubeClient: kubernetes.NewForConfigOrDie(config),
		Monitor:    NewMonitor(ctx, client),
	}
}

func (env *Environment) Stop() {
	env.cancel()
}

func NewConfig() *rest.Config {
	config := controllerruntime.GetConfigOrDie()
	config.UserAgent = fmt.Sprintf("testing-%s", operator.Version)
	config.QPS = 1e6
	config.Burst = 1e6
	return config
}

func NewClient(ctx context.Context, config *rest.Config) client.Client {
	scheme := runtime.NewScheme()
	lo.Must0(clientgoscheme.AddToScheme(scheme))
	lo.Must0(apis.AddToScheme(scheme))
	lo.Must0(coreapis.AddToScheme(scheme))

	cache := lo.Must(cache.New(config, cache.Options{Scheme: scheme}))
	lo.Must0(cache.IndexField(ctx, &v1.Pod{}, "spec.nodeName", func(o client.Object) []string {
		pod := o.(*v1.Pod)
		return []string{pod.Spec.NodeName}
	}))
	lo.Must0(cache.IndexField(ctx, &v1.Event{}, "involvedObject.kind", func(o client.Object) []string {
		evt := o.(*v1.Event)
		return []string{evt.InvolvedObject.Kind}
	}))
	lo.Must0(cache.IndexField(ctx, &v1.Node{}, "spec.unschedulable", func(o client.Object) []string {
		node := o.(*v1.Node)
		return []string{strconv.FormatBool(node.Spec.Unschedulable)}
	}))
	lo.Must0(cache.IndexField(ctx, &v1.Node{}, "spec.taints[*].karpenter.sh/disruption", func(o client.Object) []string {
		node := o.(*v1.Node)
		t, _ := lo.Find(node.Spec.Taints, func(t v1.Taint) bool {
			return t.Key == corev1beta1.DisruptionTaintKey
		})
		return []string{t.Value}
	}))
	c := lo.Must(client.New(config, client.Options{Scheme: scheme, Cache: &client.CacheOptions{Reader: cache}}))

	go func() {
		lo.Must0(cache.Start(ctx))
	}()
	if !cache.WaitForCacheSync(ctx) {
		log.Fatalf("cache failed to sync")
	}
	return c
}
<<<<<<< HEAD
=======

func (env *Environment) DefaultNodePool(nodeClass *v1alpha2.AKSNodeClass) *karpv1.NodePool {
	nodePool := coretest.NodePool()
	nodePool.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
		Group: object.GVK(nodeClass).Group,
		Kind:  object.GVK(nodeClass).Kind,
		Name:  nodeClass.Name,
	}
	nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelOSStable,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{string(corev1.Linux)},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:	v1alpha2.LabelSKUVersion, 
				Operator: corev1.NodeSelectorOpLt,
				Values: []string{"6"},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeOnDemand},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelArchStable,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{karpv1.ArchitectureAmd64},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1alpha2.LabelSKUFamily,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"D"},
			},
		},
	}

	nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
	nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")
	nodePool.Spec.Template.Spec.ExpireAfter.Duration = nil
	nodePool.Spec.Limits = karpv1.Limits(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1000"), // TODO: do we need that much?
		corev1.ResourceMemory: resource.MustParse("1000Gi"),
	})

	// TODO: make this conditional on Cilium
	// https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
	nodePool.Spec.Template.Spec.StartupTaints = append(nodePool.Spec.Template.Spec.StartupTaints, corev1.Taint{
		Key:    "node.cilium.io/agent-not-ready",
		Effect: corev1.TaintEffectNoExecute,
		Value:  "true",
	})
	// # required for Karpenter to predict overhead from cilium DaemonSet
	nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{
		"kubernetes.azure.com/ebpf-dataplane": "cilium",
	})
	return nodePool
}

func (env *Environment) ArmNodepool(nodeClass *v1alpha2.AKSNodeClass) *karpv1.NodePool {
	nodePool := env.DefaultNodePool(nodeClass)
	coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
		NodeSelectorRequirement: corev1.NodeSelectorRequirement{
			Key:      corev1.LabelArchStable,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{karpv1.ArchitectureArm64},
		}})
	return nodePool
}
>>>>>>> 7e01903 (test: removing environment checks that validate against karpenter pod/settings)
