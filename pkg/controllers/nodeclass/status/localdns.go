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

package status

import (
	"context"
	"fmt"
	"regexp"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

type LocalDNSReconciler struct{}

func NewLocalDNSReconciler() *LocalDNSReconciler {
	return &LocalDNSReconciler{}
}

const (
	LocalDNSUnreadyReasonInvalidConfiguration = "InvalidConfiguration"
	localDNSReconcilerName                    = "nodeclass.localdns"
)

var (
	zoneRegex = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.?$`)
)

func (r *LocalDNSReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithName(localDNSReconcilerName)

	// If LocalDNS is not configured, mark as ready (it's optional)
	if nodeClass.Spec.LocalDNS == nil {
		nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
		return reconcile.Result{}, nil
	}

	// Run all validations
	if err := r.validate(logger, nodeClass.Spec.LocalDNS); err != nil {
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeLocalDNSReady,
			err.reason,
			err.message,
		)
		return reconcile.Result{}, nil
	}

	// All validations passed
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeLocalDNSReady)
	return reconcile.Result{}, nil
}

type validationError struct {
	reason  string
	message string
}

func (r *LocalDNSReconciler) validate(logger logr.Logger, localDNS *v1beta1.LocalDNS) *validationError {
	// Validate VnetDNSOverrides
	if err := r.validateOverridesMap(logger, localDNS.VnetDNSOverrides); err != nil {
		return err
	}

	// Validate KubeDNSOverrides
	if err := r.validateOverridesMap(logger, localDNS.KubeDNSOverrides); err != nil {
		return err
	}

	return nil
}

func (r *LocalDNSReconciler) validateOverridesMap(logger logr.Logger, overrides map[string]*v1beta1.LocalDNSOverrides) *validationError {
	if overrides == nil {
		return nil
	}
	// Validate zone name format
	for zone := range overrides {
		if zone != "." && !zoneRegex.MatchString(zone) {
			logger.Info("Invalid zone name format", "zone", zone)
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidConfiguration,
				message: fmt.Sprintf("Invalid zone name format: '%s'. Zone names must be valid DNS labels (alphanumeric with hyphens/underscores, max 63 characters per label, cannot start/end with hyphen or underscore)", zone),
			}
		}
	}
	return nil
}
