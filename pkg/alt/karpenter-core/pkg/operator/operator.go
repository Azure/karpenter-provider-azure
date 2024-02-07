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

// Source: github.com/aws/karpenter-core@v0.30.0/pkg/operator/operator.go

package operator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-logr/zapr"
	"github.com/samber/lo"

	"sigs.k8s.io/karpenter/pkg/apis/v1alpha5"
	"sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/events"

	coreoperator "sigs.k8s.io/karpenter/pkg/operator"
	coreoperatorlogging "sigs.k8s.io/karpenter/pkg/operator/logging"

	coordinationv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	"sigs.k8s.io/karpenter/pkg/operator/options"
	"sigs.k8s.io/karpenter/pkg/operator/scheme"

	//"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/utils/clock"
	knativeinjection "knative.dev/pkg/injection"
	knativelogging "knative.dev/pkg/logging"
	"knative.dev/pkg/signals"
	"knative.dev/pkg/system"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// Unmodified; for exposing private entity only
const (
	appName   = "karpenter"
	component = "controller"
)

// Source: NewOperator()
// Modified behavior:
// - Allow Karpenter and most components to exist on control plane, but can reach the CRs on overlay
// - Webhooks not supported
// - Karpenter will not crash if CRDs are not found, but goes into a retry loop for a while
// Modified implementations:
// - Split the context into two: control plane and overlay
// - Remove webhooks-related code
// - Retry loop for getting CRDs
// - Introduce and retrieve overlay namespace from env
// - No profiling
// The code is purposefully kept in the similar structure as the original for easy comparison
// nolint:revive,stylecheck
func NewOperator() (context.Context, *coreoperator.Operator) {
	overlayNamespace := os.Getenv("OVERLAY_NAMESPACE")
	if overlayNamespace == "" {
		overlayNamespace = "karpenter-system"
	}

	// Root Context
	originCtx := signals.NewContext()
	ccPlaneCtx := knativeinjection.WithNamespaceScope(originCtx, system.Namespace())
	overlayCtx := knativeinjection.WithNamespaceScope(originCtx, overlayNamespace)

	// Options
	ccPlaneCtx = injection.WithOptionsOrDie(ccPlaneCtx, options.Injectables...)
	overlayCtx = injection.WithOptionsOrDie(overlayCtx, options.Injectables...)

	// Make the Karpenter binary aware of the container memory limit
	// https://pkg.go.dev/runtime/debug#SetMemoryLimit
	if options.FromContext(ccPlaneCtx).MemoryLimit > 0 {
		newLimit := int64(float64(options.FromContext(ccPlaneCtx).MemoryLimit) * 0.9)
		debug.SetMemoryLimit(newLimit)
	}

	// Webhook
	// Unsupported -- skipping

	// Client Config
	ccPlaneConfig := lo.Must(rest.InClusterConfig())
	overlayConfig := controllerruntime.GetConfigOrDie()
	ccPlaneConfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(options.FromContext(ccPlaneCtx).KubeClientQPS), options.FromContext(ccPlaneCtx).KubeClientBurst)
	overlayConfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(options.FromContext(overlayCtx).KubeClientQPS), options.FromContext(overlayCtx).KubeClientBurst)
	// config.UserAgent = fmt.Sprintf("%s/%s", appName, Version)
	ccPlaneConfig.UserAgent = appName
	overlayConfig.UserAgent = appName

	// Client
	overlayKubernetesInterface := kubernetes.NewForConfigOrDie(overlayConfig)
	//configMapWatcher := informer.NewInformedWatcher(ccPlaneKubernetesInterface, system.Namespace())
	//lo.Must0(configMapWatcher.Start(ccPlaneCtx.Done()))

	// Logging
	logger := coreoperatorlogging.NewLogger(ccPlaneCtx, component)
	ccPlaneCtx = knativelogging.WithLogger(ccPlaneCtx, logger)
	overlayCtx = knativelogging.WithLogger(overlayCtx, logger)
	coreoperatorlogging.ConfigureGlobalLoggers(ccPlaneCtx)

	// Manager
	mgrOpts := controllerruntime.Options{
		Logger:                     ignoreDebugEvents(zapr.NewLogger(logger.Desugar())),
		LeaderElection:             options.FromContext(overlayCtx).EnableLeaderElection,
		LeaderElectionID:           "karpenter-leader-election",
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		Scheme:                     scheme.Scheme,
		Metrics: server.Options{
			BindAddress: fmt.Sprintf(":%d", options.FromContext(overlayCtx).MetricsPort),
		},
		HealthProbeBindAddress: fmt.Sprintf(":%d", options.FromContext(overlayCtx).HealthProbePort),
		BaseContext: func() context.Context {
			ctx := context.Background()
			ctx = knativelogging.WithLogger(ctx, logger)
			// ctx = injection.WithConfig(ctx, overlayConfig)
			ctx = injection.WithOptionsOrDie(ctx, options.Injectables...)
			return ctx
		},
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&coordinationv1.Lease{}: {
					Field: fields.SelectorFromSet(fields.Set{"metadata.namespace": "kube-node-lease"}),
				},
			},
		},
	}
	if options.FromContext(ccPlaneCtx).EnableProfiling {
		mgrOpts.Metrics.ExtraHandlers = lo.Assign(mgrOpts.Metrics.ExtraHandlers, map[string]http.Handler{
			"/debug/pprof/":             http.HandlerFunc(pprof.Index),
			"/debug/pprof/cmdline":      http.HandlerFunc(pprof.Cmdline),
			"/debug/pprof/profile":      http.HandlerFunc(pprof.Profile),
			"/debug/pprof/symbol":       http.HandlerFunc(pprof.Symbol),
			"/debug/pprof/trace":        http.HandlerFunc(pprof.Trace),
			"/debug/pprof/allocs":       pprof.Handler("allocs"),
			"/debug/pprof/heap":         pprof.Handler("heap"),
			"/debug/pprof/block":        pprof.Handler("block"),
			"/debug/pprof/goroutine":    pprof.Handler("goroutine"),
			"/debug/pprof/threadcreate": pprof.Handler("threadcreate"),
		})
	}
	mgr, err := controllerruntime.NewManager(overlayConfig, mgrOpts)
	mgr = lo.Must(mgr, err, "failed to setup manager")

	lo.Must0(mgr.GetFieldIndexer().IndexField(overlayCtx, &v1.Pod{}, "spec.nodeName", func(o client.Object) []string {
		return []string{o.(*v1.Pod).Spec.NodeName}
	}), "failed to setup pod indexer")
	lo.Must0(mgr.GetFieldIndexer().IndexField(overlayCtx, &v1.Node{}, "spec.providerID", func(o client.Object) []string {
		return []string{o.(*v1.Node).Spec.ProviderID}
	}), "failed to setup node provider id indexer")
	lo.Must0(func() error {
		_, _, err := lo.AttemptWithDelay(42, 10*time.Second, func(index int, duration time.Duration) error {
			err := mgr.GetFieldIndexer().IndexField(overlayCtx, &v1beta1.NodeClaim{}, "status.providerID", func(o client.Object) []string {
				return []string{o.(*v1beta1.NodeClaim).Status.ProviderID}
			})
			if err != nil {
				knativelogging.FromContext(ccPlaneCtx).Infof("failed to setup machine provider id indexer, CRDs deployment may not be ready, index: %d, duration: %v, err: %v", index, duration, err)
			}
			return err
		})
		return err
	}(), "failed to setup nodeclaim provider id indexer, all attempts used")
	lo.Must0(mgr.GetFieldIndexer().IndexField(overlayCtx, &v1alpha5.Machine{}, "status.providerID", func(o client.Object) []string {
		return []string{o.(*v1alpha5.Machine).Status.ProviderID}
	}), "failed to setup machine provider id indexer")

	lo.Must0(mgr.AddReadyzCheck("manager", func(req *http.Request) error {
		return lo.Ternary(mgr.GetCache().WaitForCacheSync(req.Context()), nil, fmt.Errorf("failed to sync caches"))
	}))
	lo.Must0(mgr.AddHealthzCheck("healthz", healthz.Ping))
	lo.Must0(mgr.AddReadyzCheck("readyz", healthz.Ping))

	return ccPlaneCtx, &coreoperator.Operator{
		Manager:             mgr,
		KubernetesInterface: overlayKubernetesInterface,
		EventRecorder:       events.NewRecorder(mgr.GetEventRecorderFor(appName)),
		Clock:               clock.RealClock{},
	}
}
