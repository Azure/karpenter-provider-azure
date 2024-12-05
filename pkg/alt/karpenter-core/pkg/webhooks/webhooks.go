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
package webhooks

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/alt/knative/pkg/webhook/resourcesemantics/conversion"
	"github.com/awslabs/operatorpkg/object"
	"github.com/samber/lo"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime/schema"
	informers "k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	knativeinjection "knative.dev/pkg/injection"
	kubeinformerfactory "knative.dev/pkg/injection/clients/namespacedkube/informers/factory"
	"knative.dev/pkg/injection/sharedmain"
	knativelogging "knative.dev/pkg/logging"
	"knative.dev/pkg/metrics"
	"knative.dev/pkg/webhook"
	"knative.dev/pkg/webhook/certificates"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	"sigs.k8s.io/karpenter/pkg/operator/logging"
	"sigs.k8s.io/karpenter/pkg/operator/options"
)

const component = "webhook"

var (
	// TODO: Remove conversion webhooks once support for the v1beta1 APIs is dropped
	ConversionResource = map[schema.GroupKind]conversion.GroupKindConversion{
		object.GVK(&v1beta1.NodePool{}).GroupKind(): {
			DefinitionName: "nodepools.karpenter.sh",
			HubVersion:     "v1",
			Zygotes: map[string]conversion.ConvertibleObject{
				"v1":      &v1.NodePool{},
				"v1beta1": &v1beta1.NodePool{},
			},
		},
		object.GVK(&v1beta1.NodeClaim{}).GroupKind(): {
			DefinitionName: "nodeclaims.karpenter.sh",
			HubVersion:     "v1",
			Zygotes: map[string]conversion.ConvertibleObject{
				"v1":      &v1.NodeClaim{},
				"v1beta1": &v1beta1.NodeClaim{},
			},
		},
	}
)

func NewWebhooks() []knativeinjection.ControllerConstructor {
	return []knativeinjection.ControllerConstructor{
		certificates.NewController,
		NewCRDConversionWebhook,
	}
}

func NewCRDConversionWebhook(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	nodeclassCtx := injection.GetNodeClasses(ctx)
	client := injection.GetClient(ctx)
	return conversion.NewConversionController(
		ctx,
		"/conversion/karpenter.sh",
		ConversionResource,
		func(ctx context.Context) context.Context {
			return injection.WithClient(injection.WithNodeClasses(ctx, nodeclassCtx), client)
		},
	)
}

// Start copies the relevant portions for starting the webhooks from sharedmain.MainWithConfig
// https://github.com/knative/pkg/blob/0f52db700d63/injection/sharedmain/main.go#L227
func Start(ctx context.Context, cfg *rest.Config, ctors ...knativeinjection.ControllerConstructor) {
	logger := logging.NewLogger(ctx, component).Sugar()
	ctx = knativelogging.WithLogger(ctx, logger)

	// AKS customized
	// This section is customized for AKS use-case to allow overriding the informer factory.
	// Overriding the informer factory is required to make webhooks work, as it is used
	// in webhook.New() to construct a new secret client, see:
	// https://github.com/knative/pkg/blob/main/webhook/webhook.go#L183
	var overriddenInformerFactory informers.SharedInformerFactory
	if ctx.Value(kubeinformerfactory.Key{}) != nil {
		overriddenInformerFactory = kubeinformerfactory.Get(ctx)
	}
	// end customization

	ctx, startInformers := knativeinjection.EnableInjectionOrDie(ctx, cfg)
	cmw := sharedmain.SetupConfigMapWatchOrDie(ctx, logger)
	controllers, webhooks := sharedmain.ControllersAndWebhooksFromCtors(ctx, cmw, ctors...)

	// AKS customized
	if overriddenInformerFactory != nil {
		ctx = context.WithValue(ctx, kubeinformerfactory.Key{}, overriddenInformerFactory)
	}
	// end customization

	// Many of the webhooks rely on configuration, e.g. configurable defaults, feature flags.
	// So make sure that we have synchronized our configuration state before launching the
	// webhooks, so that things are properly initialized.
	logger.Info("Starting configuration manager...")
	if err := cmw.Start(ctx.Done()); err != nil {
		logger.Fatalw("Failed to start configuration manager", zap.Error(err))
	}

	// If we have one or more admission controllers, then start the webhook
	// and pass them in.
	var wh *webhook.Webhook
	var err error
	eg, egCtx := errgroup.WithContext(ctx)
	if len(webhooks) > 0 {
		// Update the metric exporter to point to a prometheus endpoint
		lo.Must0(metrics.UpdateExporter(ctx, metrics.ExporterOptions{
			Component:      strings.ReplaceAll(component, "-", "_"),
			ConfigMap:      lo.Must(metrics.NewObservabilityConfigFromConfigMap(nil)).GetConfigMap().Data,
			Secrets:        sharedmain.SecretFetcher(ctx),
			PrometheusPort: options.FromContext(ctx).WebhookMetricsPort,
		}, logger))
		// Register webhook metrics
		webhook.RegisterMetrics()

		wh, err = webhook.New(ctx, webhooks)
		if err != nil {
			logger.Fatalw("Failed to create webhook", zap.Error(err))
		}
		eg.Go(func() error {
			return wh.Run(ctx.Done())
		})
	}

	// Start the injection clients and informers.
	startInformers()

	// Wait for webhook informers to sync.
	if wh != nil {
		wh.InformersHaveSynced()
	}
	logger.Info("Starting controllers...")
	eg.Go(func() error {
		return controller.StartAll(ctx, controllers...)
	})
	// This will block until either a signal arrives or one of the grouped functions
	// returns an error.
	<-egCtx.Done()

	// Don't forward ErrServerClosed as that indicates we're already shutting down.
	if err := eg.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Errorw("Error while running server", zap.Error(err))
	}
}

func HealthProbe(ctx context.Context) healthz.Checker {
	// Create new transport that doesn't validate the TLS certificate
	// This transport is just polling so validating the server certificate isn't necessary
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // nolint:gosec
	client := &http.Client{Transport: transport}

	// TODO: Add knative health check port for webhooks when health port can be configured
	// Issue: https://github.com/knative/pkg/issues/2765
	return func(req *http.Request) (err error) {
		res, err := client.Get(fmt.Sprintf("https://localhost:%d", options.FromContext(ctx).WebhookPort))
		// If the webhook connection errors out, liveness/readiness should fail
		if err != nil {
			return err
		}
		// Close the body to avoid leaking file descriptors
		// Always read the body so we can re-use the connection: https://stackoverflow.com/questions/17948827/reusing-http-connections-in-go
		_, _ = io.ReadAll(res.Body)
		res.Body.Close()

		// If there is a server-side error or path not found,
		// consider liveness to have failed
		if res.StatusCode >= 500 || res.StatusCode == 404 {
			return fmt.Errorf("webhook probe failed with status code %d", res.StatusCode)
		}
		return nil
	}
}
