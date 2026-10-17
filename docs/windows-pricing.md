# Windows offering pricing

Karpenter uses the AKS cluster Windows license type to choose the public price for Windows offerings:

| Node operating system | AKS `windowsProfile.licenseType` | Price used by Karpenter |
|---|---|---|
| Linux | Any supported value | Base compute |
| Windows | `None` | Windows license included |
| Windows | `Windows_Server` | Base compute (Azure Hybrid Benefit) |

This setting changes Karpenter's scheduling and consolidation estimate only. AKS remains responsible for applying the license type to nodes. See [Use Azure Hybrid Benefit in AKS](https://learn.microsoft.com/azure/aks/azure-hybrid-benefit) for licensing requirements and cluster configuration.

## Self-hosted Karpenter

Set the cluster value in the Helm values:

```yaml
settings:
  windowsLicenseType: None # or Windows_Server
```

`configure-values.sh` reads the value from the AKS cluster and writes `settings.windowsLicenseType` automatically. Use the script, template, and chart from the same release; mixing versions can omit settings or pass unsupported arguments.

Existing self-hosted installations must set this value before upgrading to a release that includes Windows-aware pricing. Regenerate the values with the release-matched `configure-values.sh`, or set the Helm value explicitly, before running `helm upgrade`.

If you enable or disable Azure Hybrid Benefit after installing self-hosted Karpenter, regenerate the values and run `helm upgrade` again so Karpenter's estimate remains aligned with the cluster.

Managed NAP supplies the cluster setting through its managed deployment configuration.
