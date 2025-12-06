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
	"strings"

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
	zoneRoot                                  = "."
	zoneClusterLocal                          = "cluster.local"
	fieldVnetDNSOverrides                     = "vnetDNSOverrides"
	fieldKubeDNSOverrides                     = "kubeDNSOverrides"
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
	// Validate VnetDNSOverrides (isVnetDNS=true enables extra forwarding restriction)
	if err := r.validateOverridesList(logger, localDNS.VnetDNSOverrides, fieldVnetDNSOverrides, true); err != nil {
		return err
	}

	// Validate KubeDNSOverrides
	if err := r.validateOverridesList(logger, localDNS.KubeDNSOverrides, fieldKubeDNSOverrides, false); err != nil {
		return err
	}

	return nil
}

func (r *LocalDNSReconciler) validateOverridesList(logger logr.Logger, overrides []v1beta1.LocalDNSZoneOverride, fieldName string, isVnetDNS bool) *validationError {
	if len(overrides) == 0 {
		return nil
	}

	seenZones := make(map[string]bool)

	for _, override := range overrides {
		// Check for duplicate zones
		if seenZones[override.Zone] {
			logger.Info("Duplicate zone found", "zone", override.Zone, "field", fieldName)
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidConfiguration,
				message: fmt.Sprintf("Duplicate zone '%s' in %s. Each zone must be unique.", override.Zone, fieldName),
			}
		}
		seenZones[override.Zone] = true

		// Validate zone name format
		if err := validateZoneForCoreDNSConfigMap(override.Zone); err != nil {
			logger.Info("Invalid zone name format", "zone", override.Zone, "error", err)
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidConfiguration,
				message: err.Error(),
			}
		}

		// Validate zone-specific rules
		if err := validateOverrideForZone(override, isVnetDNS); err != nil {
			logger.Info("Invalid override for zone", "zone", override.Zone, "error", err)
			return &validationError{
				reason:  LocalDNSUnreadyReasonInvalidConfiguration,
				message: err.Error(),
			}
		}
	}

	// Check required zones
	if !seenZones[zoneRoot] || !seenZones[zoneClusterLocal] {
		logger.Info("Missing required zones", "field", fieldName, "hasRootZone", seenZones[zoneRoot], "hasClusterLocal", seenZones[zoneClusterLocal])
		return &validationError{
			reason:  LocalDNSUnreadyReasonInvalidConfiguration,
			message: fmt.Sprintf("%s must contain required zones '%s' and '%s'", fieldName, zoneRoot, zoneClusterLocal),
		}
	}

	return nil
}

// validateZoneForCoreDNSConfigMap validates that a zone name is valid for use in a CoreDNS ConfigMap.
// The root zone "." is always valid. Other zones must be valid DNS labels.
func validateZoneForCoreDNSConfigMap(zone string) error {
	if zone == zoneRoot {
		return nil
	}
	if !zoneRegex.MatchString(zone) {
		return fmt.Errorf("invalid zone name format: '%s'. Zone names must be valid DNS labels (alphanumeric with hyphens/underscores, max 63 characters per label, cannot start/end with hyphen or underscore)", zone)
	}
	return nil
}

// validateOverrideForZone validates zone-specific rules.
func validateOverrideForZone(override v1beta1.LocalDNSZoneOverride, isVnetDNS bool) error {
	// Reject forwarding "cluster.local" to VnetDNS
	if strings.HasSuffix(override.Zone, zoneClusterLocal) && override.ForwardDestination == v1beta1.LocalDNSForwardDestinationVnetDNS {
		return fmt.Errorf("DNS traffic for '%s' cannot be forwarded to VnetDNS", override.Zone)
	}
	// Reject forwarding root "." to ClusterCoreDNS from vnetDNSOverrides
	if isVnetDNS && override.Zone == zoneRoot && override.ForwardDestination == v1beta1.LocalDNSForwardDestinationClusterCoreDNS {
		return fmt.Errorf("DNS traffic for root zone cannot be forwarded to ClusterCoreDNS")
	}
	// Reject ServeStale verify when TCP protocol is used
	if override.ServeStale == v1beta1.LocalDNSServeStaleVerify && override.Protocol == v1beta1.LocalDNSProtocolForceTCP {
		return fmt.Errorf("ServeStale verify cannot be used with ForceTCP protocol for zone '%s'", override.Zone)
	}
	return nil
}
