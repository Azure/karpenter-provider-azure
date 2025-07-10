---
title: "Upgrade Guide"
linkTitle: "Upgrade Guide"
weight: 10
description: >
  Learn how to upgrade Karpenter for Azure
---

This guide covers how to upgrade Karpenter for Azure to newer versions, including both minor and major version upgrades.

## Before You Begin

### Prerequisites

- Access to your AKS cluster with admin permissions
- Helm 3.x installed
- Azure CLI installed and authenticated
- Backup of current Karpenter configuration

### Review Release Notes

Always review the [release notes](https://github.com/Azure/karpenter-provider-azure/releases) before upgrading to understand:
- Breaking changes
- New features
- Bug fixes
- Known issues

## Upgrade Process

### 1. Backup Current Configuration

Export your current Karpenter configuration:

```bash
# Export NodePools
kubectl get nodepools -o yaml > nodepools-backup.yaml

# Export AKSNodeClasses
kubectl get aksnodeclasses -o yaml > aksnodeclasses-backup.yaml

# Export Karpenter deployment
helm get values karpenter -n karpenter > karpenter-values-backup.yaml
```

### 2. Check Version Compatibility

Verify compatibility between:
- Karpenter version and Kubernetes version
- Karpenter version and AKS version
- NodePool/AKSNodeClass API versions

```bash
# Check current Karpenter version
kubectl get deployment karpenter -n karpenter -o jsonpath='{.spec.template.spec.containers[0].image}'

# Check Kubernetes version
kubectl version --short
```

### 3. Update Helm Repository

```bash
# Update Karpenter Helm repository
helm repo update

# Check available versions
helm search repo karpenter --versions
```

### 4. Perform the Upgrade

#### Minor Version Upgrades (e.g., v0.5.1 to v0.5.2)

Minor version upgrades typically include bug fixes and small improvements:

```bash
# Upgrade Karpenter
helm upgrade karpenter oci://mcr.microsoft.com/aks/karpenter/karpenter \
  --version "0.5.2" \
  --namespace karpenter \
  --reuse-values
```

#### Major Version Upgrades (e.g., v0.5.x to v0.6.x)

Major version upgrades may include breaking changes and require more careful planning:

1. **Review breaking changes** in the release notes
2. **Test in a non-production environment** first
3. **Update CRD definitions** if necessary:

```bash
# Update CRDs if required
kubectl apply -f https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v0.6.0/charts/karpenter/crds/
```

4. **Upgrade Helm chart**:

```bash
helm upgrade karpenter oci://mcr.microsoft.com/aks/karpenter/karpenter \
  --version "0.6.0" \
  --namespace karpenter \
  --values karpenter-values.yaml
```

### 5. Verify the Upgrade

```bash
# Check Karpenter pod status
kubectl get pods -n karpenter

# Verify new version
kubectl get deployment karpenter -n karpenter -o jsonpath='{.spec.template.spec.containers[0].image}'

# Check controller logs for errors
kubectl logs -n karpenter deployment/karpenter -f

# Verify NodePools and AKSNodeClasses are still valid
kubectl get nodepools,aksnodeclasses
```

### 6. Test Functionality

After upgrading, test core functionality:

```bash
# Create a test pod that requires new capacity
kubectl run test-pod --image=nginx --restart=Never

# Watch for node provisioning
kubectl get nodes -w

# Clean up test pod
kubectl delete pod test-pod
```

## Rollback Process

If issues occur after upgrading, you can rollback:

### 1. Rollback Helm Deployment

```bash
# Check rollback history
helm history karpenter -n karpenter

# Rollback to previous version
helm rollback karpenter -n karpenter
```

### 2. Restore Configuration

If needed, restore previous configuration:

```bash
# Restore NodePools
kubectl apply -f nodepools-backup.yaml

# Restore AKSNodeClasses  
kubectl apply -f aksnodeclasses-backup.yaml
```

## Version-Specific Upgrade Notes

### Upgrading to v0.6.x

**Breaking Changes:**
- New AKSNodeClass API version (v1beta1)
- Changes to default image families
- Updated Azure API usage

**Migration Steps:**
1. Update AKSNodeClass resources to v1beta1 API
2. Review image family settings
3. Verify Azure permissions include new API calls

### Upgrading to v0.5.x

**New Features:**
- Enhanced spot VM support
- Improved consolidation algorithms
- Additional Azure VM size support

**Migration Steps:**
1. Review new spot VM configuration options
2. Update consolidation policies if desired

## Best Practices

### Pre-Upgrade Testing

- **Test in staging environment** with similar workloads
- **Verify backup and restore procedures**
- **Plan maintenance windows** for production upgrades
- **Communicate with stakeholders** about planned downtime

### Upgrade Strategy

- **Gradual rollout**: Upgrade non-critical environments first
- **Monitor closely**: Watch logs and metrics during and after upgrade
- **Have rollback plan**: Ensure you can quickly revert if needed
- **Version pinning**: Consider pinning to specific versions in production

### Post-Upgrade Validation

- **Monitor node provisioning** for several hours
- **Check cost metrics** for unexpected changes
- **Validate autoscaling behavior** under load
- **Review security posture** after upgrade

## Troubleshooting Upgrades

### Common Issues

**CRD Version Conflicts**
```bash
# Check CRD versions
kubectl get crd nodepools.karpenter.sh -o yaml | grep -A5 versions

# Update CRDs manually if needed
kubectl apply -f https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v0.6.0/charts/karpenter/crds/
```

**Pod Stuck in Pending**
```bash
# Check NodePool compatibility
kubectl describe pod <stuck-pod>

# Verify NodePool requirements
kubectl get nodepools -o yaml
```

**Permission Errors**
```bash
# Check Azure permissions
az role assignment list --assignee <karpenter-identity>

# Update permissions if needed
az role assignment create --assignee <karpenter-identity> --role "Virtual Machine Contributor"
```

### Getting Help

If you encounter issues during upgrade:

1. **Check troubleshooting guide**: Review the [troubleshooting documentation](../troubleshooting)
2. **Search existing issues**: Look for similar problems in [GitHub issues](https://github.com/Azure/karpenter-provider-azure/issues)
3. **Create new issue**: If needed, create a detailed issue report including:
   - Current and target versions
   - Error messages and logs
   - Configuration files
   - Steps to reproduce

## Automated Upgrades

For production environments, consider implementing automated upgrade processes:

### GitOps Integration

```yaml
# Example ArgoCD Application for Karpenter
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: karpenter
spec:
  source:
    repoURL: https://charts.karpenter.sh
    chart: karpenter
    targetRevision: "0.6.0"
  destination:
    server: https://kubernetes.default.svc
    namespace: karpenter
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

### CI/CD Pipeline Integration

- **Automated testing**: Include Karpenter upgrades in your testing pipeline
- **Staged deployments**: Automatically deploy to dev → staging → production
- **Monitoring integration**: Include upgrade success/failure in monitoring dashboards