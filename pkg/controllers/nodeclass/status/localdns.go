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
	LocalDNSUnreadyReasonMissingRequiredZones = "MissingRequiredZones"
	LocalDNSUnreadyReasonInvalidForwarding    = "InvalidForwarding"

	localDNSReconcilerName = "nodeclass.localdns"
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
	if err := r.validateOverridesMap(logger, localDNS.VnetDNSOverrides, "vnetDNSOverrides"); err != nil {
		return err
	}

	// Validate KubeDNSOverrides
	if err := r.validateOverridesMap(logger, localDNS.KubeDNSOverrides, "kubeDNSOverrides"); err != nil {
		return err
	}

	return nil
}

func (r *LocalDNSReconciler) validateOverridesMap(logger logr.Logger, overrides map[string]*v1beta1.LocalDNSOverrides, fieldName string) *validationError {
	if overrides == nil {
		return nil
	}

	// Check required zones
	if err := validateRequiredZones(overrides, fieldName); err != nil {
		logger.Info("Required zones validation failed", "field", fieldName, "error", err.Error())
		return &validationError{
			reason:  LocalDNSUnreadyReasonMissingRequiredZones,
			message: err.Error(),
		}
	}

	// Check forwarding rules for VnetDNS
	if fieldName == "vnetDNSOverrides" {
		if err := r.validateVnetForwardingRules(logger, overrides); err != nil {
			return err
		}
	}

	// Validate serveStale and protocol combinations
	for zone, override := range overrides {
		if err := validateServeStaleProtocolCombination(override); err != nil {
			logger.Info("Invalid configuration", "field", fieldName, "zone", zone, "error", err.Error())
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidConfiguration,
				message: fmt.Sprintf("%s zone '%s': %s", fieldName, zone, err.Error()),
			}
		}
	}

	return nil
}

func (r *LocalDNSReconciler) validateVnetForwardingRules(logger logr.Logger, overrides map[string]*v1beta1.LocalDNSOverrides) *validationError {
	// Validate that cluster.local is not forwarded to VnetDNS
	if override, exists := overrides["cluster.local"]; exists && override != nil && override.ForwardDestination != nil {
		if *override.ForwardDestination == v1beta1.LocalDNSForwardDestinationVnetDNS {
			logger.Info("Invalid VnetDNSOverrides: cluster.local cannot be forwarded to VnetDNS")
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidForwarding,
				message: "DNS traffic for 'cluster.local' cannot be forwarded to VnetDNS",
			}
		}
	}

	// Validate that root zone '.' is not forwarded to ClusterCoreDNS
	if override, exists := overrides["."]; exists && override != nil && override.ForwardDestination != nil {
		if *override.ForwardDestination == v1beta1.LocalDNSForwardDestinationClusterCoreDNS {
			logger.Info("Invalid VnetDNSOverrides: root zone '.' cannot be forwarded to ClusterCoreDNS")
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidForwarding,
				message: "DNS traffic for root zone '.' cannot be forwarded to ClusterCoreDNS in vnetDNSOverrides",
			}
		}
	}

	return nil
}

// validateRequiredZones checks if the required zones '.' and 'cluster.local' are present
func validateRequiredZones(overrides map[string]*v1beta1.LocalDNSOverrides, fieldName string) error {
	requiredZones := []string{".", "cluster.local"}
	for _, zone := range requiredZones {
		if _, exists := overrides[zone]; !exists {
			return fmt.Errorf("%s must contain required zones '.' and 'cluster.local'", fieldName)
		}
	}
	return nil
}

// validateServeStaleProtocolCombination validates that serveStale 'Verify' is not used with protocol 'ForceTCP'
func validateServeStaleProtocolCombination(overrides *v1beta1.LocalDNSOverrides) error {
	if overrides == nil {
		return nil
	}

	if overrides.ServeStale != nil && *overrides.ServeStale == v1beta1.LocalDNSServeStaleVerify &&
		overrides.Protocol != nil && *overrides.Protocol == v1beta1.LocalDNSProtocolForceTCP {
		return fmt.Errorf("serveStale 'Verify' cannot be used with protocol 'ForceTCP'")
	}

	return nil
}
