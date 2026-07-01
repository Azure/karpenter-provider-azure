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

package bootstraptoken

import (
	"context"
	"fmt"
	"time"

	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
)

const (
	// BootstrapTokenRefreshInterval is the interval at which the controller
	// reconciles to check for bootstrap token changes.
	BootstrapTokenRefreshInterval = 1 * time.Hour

	// bootstrapTokenSecretType is the secret type for bootstrap tokens.
	bootstrapTokenSecretType = "bootstrap.kubernetes.io/token"
)

// Controller periodically reads bootstrap token secrets from kube-system
// namespace and updates the options with the token value. This removes
// the need to pass the bootstrap token via Helm chart values.
type Controller struct {
	kubernetesInterface kubernetes.Interface
	options             *options.Options
}

func NewController(kubernetesInterface kubernetes.Interface, opts *options.Options) *Controller {
	return &Controller{
		kubernetesInterface: kubernetesInterface,
		options:             opts,
	}
}

func (c *Controller) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "bootstraptoken")

	token, err := c.readBootstrapToken(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, "reading bootstrap token from kube-system secrets")
		return reconciler.Result{}, err
	}

	c.options.KubeletClientTLSBootstrapToken = token
	log.FromContext(ctx).V(1).Info("updated bootstrap token from kube-system secret")
	return reconciler.Result{RequeueAfter: BootstrapTokenRefreshInterval}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("bootstraptoken").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}

// readBootstrapToken reads the first bootstrap token secret from kube-system
// namespace and constructs the token from the token-id and token-secret data fields.
func (c *Controller) readBootstrapToken(ctx context.Context) (string, error) {
	secrets, err := c.kubernetesInterface.CoreV1().Secrets("kube-system").List(ctx, metav1.ListOptions{
		FieldSelector: "type=" + bootstrapTokenSecretType,
	})
	if err != nil {
		return "", fmt.Errorf("listing bootstrap token secrets: %w", err)
	}

	if len(secrets.Items) == 0 {
		return "", fmt.Errorf("no bootstrap token secrets found in kube-system namespace")
	}

	secret := secrets.Items[0]
	tokenID, ok := secret.Data["token-id"]
	if !ok {
		return "", fmt.Errorf("bootstrap token secret %q missing token-id field", secret.Name)
	}

	tokenSecret, ok := secret.Data["token-secret"]
	if !ok {
		return "", fmt.Errorf("bootstrap token secret %q missing token-secret field", secret.Name)
	}

	return fmt.Sprintf("%s.%s", string(tokenID), string(tokenSecret)), nil
}
