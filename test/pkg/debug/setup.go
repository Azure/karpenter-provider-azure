// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package debug

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	"github.com/samber/lo"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega" //nolint:revive,stylecheck
)

const (
	NoWatch  = "NoWatch"
	NoEvents = "NoEvents"
)

var m *Monitor
var e *EventClient

func BeforeEach(ctx context.Context, config *rest.Config, kubeClient client.Client) {
	// If the test is labeled as NoWatch, then the node/pod monitor will just list at the beginning
	// of the test rather than perform a watch during it
	if !lo.Contains(ginkgo.CurrentSpecReport().Labels(), NoWatch) {
		m = New(ctx, config, kubeClient)
		m.MustStart()
	}
	if !lo.Contains(ginkgo.CurrentSpecReport().Labels(), NoEvents) {
		e = NewEventClient(kubeClient)
	}
}

func AfterEach(ctx context.Context) {
	if !lo.Contains(ginkgo.CurrentSpecReport().Labels(), NoWatch) {
		m.Stop()
	}
	if !lo.Contains(ginkgo.CurrentSpecReport().Labels(), NoEvents) {
		Expect(e.DumpEvents(ctx)).To(Succeed())
	}
}
