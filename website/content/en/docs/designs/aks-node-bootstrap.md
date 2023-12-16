
---
title: "AKS Node Bootstrapping"
linkTitle: "AKS Node Bootstrapping"
weight: 10
---

Azure/AKS provider for Karpenter needs to be able to create standalone VMs that join AKS clusters. This requires both configuring Azure resources (e.g. network interface, VM, etc.) and bootstrapping the VM so that it connects to the cluster. The set of input parameters required for bootstrap is currently quite large, though there is an ongoing effort to reduce it. There are multiple sources for these parameters - from user input, to data from the cluster, to internal defaults.

The goal of this document is to describe the relevant configuration data flows and their implementation. It starts with AKS VM bootstrapping needs, works its way up to Karpenter configuration mechanisms and sources, touches on AKS cluster configuration compatibility and drift, and then describes how everything is wired together. <!-- Finally, it describes aspects of selected data flows (such as labels, or kubelet configuration).--> It stays very close to the source code, using it as the primary reference. The document is primarily focused on node bootstrapping, but it also touches on the configuration of Azure resources.

Some of the mechanisms described below are specific to AKS. However, the overall configuration flow and wiring should be flexible enough to accommodate other flavors of Kubernetes on Azure in the future.

> Note: The document uses "Karpenter" for brevity, though in some places "Azure/AKS Cloud Provider for Karpenter" might be more accurate. The distinction is not critical, especially since karpenter-core is used as library, and the build of Azure/AKS Cloud Provider for Karpenter represents Azure/AKS version of Karpenter.

## Table of Contents

- [Node bootstrapping](#node-bootstrapping)
  - [Node Bootstrapping Variables](#node-bootstrapping-variables)
- [Karpenter configuration sources](#karpenter-configuration-sources)
  - [Hardcoded values](#hardcoded-values)
  - [Environment variables](#environment-variables)
  - [Karpenter global settings](#karpenter-global-settings)
  - [Provisioner CR spec](#provisioner-cr-spec)
  - [NodeTemplate CR spec](#nodetemplate-cr-spec)
  - [Auto-detected values](#auto-detected-values)
  - [Computed values](#computed-values)
- [AKS cluster configuration compatibility and drift](#aks-cluster-configuration-compatibility-and-drift)
- [Relevant interfaces and wiring](#relevant-interfaces-and-wiring)
  - [Launch template provider](#launch-template-provider)
  - [Image provider](#image-provider)
  - [Image family](#image-family)
  - [Bootstrapper interface](#bootstrapper-interface)

## Node bootstrapping

The most common current way of bootstrapping an AKS node it by providing a highly structured [NodeBootstrappingConfiguration](https://github.com/Azure/AgentBaker/blob/3a5c5f2f2c3acd7ebcb82d73352ad6119e1522d6/pkg/agent/datamodel/types.go#L1480) to AgentBaker library to generate [Custom Script Extension](https://learn.microsoft.com/en-us/azure/virtual-machines/extensions/custom-script-linux) (CSE) and Azure [Custom Data](https://learn.microsoft.com/en-us/azure/virtual-machines/custom-data) for the VM.

A newer, emerging, approach - possible with the latest AKS VM images - is to populate Custom Data with a more streamlined set of parameters, in a well-defined format, without using CSE (one less call, and faster) and without having to use AgentBaker library. This both simplifies the bootstrapping contract, and speeds up the VM bootstrap process. Note that the set of fields and the bootstrapping contract are evolving together with corresponding support in AKS node images.

This new bootstrapping approach is used by Karpenter. In this context Karpenter is helping to drive and validate the evolution of the bootstrapping contract. Until there is a better name, this document refers to this approach as "Node Bootstrapping Variables".

### Node Bootstrapping Variables

When Karpenter creates a VM, Custom Data (often referred to as User Data) is populated with the result of rendering the template in [bootstrap/cse_cmd.sh.gtpl](/pkg/providers/imagefamily/bootstrap/cse_cmd.sh.gtpl). The template is rendered using `NodeBootstrapVariables` structure, defined in [bootstrap/aksbootstrap.go](/pkg/providers/imagefamily/bootstrap/aksbootstrap.go). The structures, variables and helper functions in that file are the primary reference for what ultimately ends up in Custom Data. `(AKS).Script` function - implementing the [Bootstrapper interface](/pkg/providers/imagefamily/bootstrap/bootstrap.go) - is the entry point, and the only inputs flowing in are the `AKS` structure fields.

The structure fields are populated from a combination of internal defaults, user input, data from the cluster, and computed values. The comments in code categorize `NodeBootstrapVariables` fields into several groups, primarily based on the source of the values, and assign them to one or more configuration sources (described below). This categorization is for informational purposes only, and is not used directly by the code - though it usually dictates the corresponding data flow.

The number of fields is expected to go down; the goal is to have a minimal set that is required and sufficient to bootstrap a VM and have it join the cluster, and the expectation is that this set will end up being quite small. (Some of the current fields may already be unnecessary, to be verified.) The format of the Custom Data may change as well, with evolution of the contract.

## Karpenter configuration sources

The following sources of bootstrap configuration are available to Karpenter:

* Hardcoded values
* Environment variables
* Karpenter global configuration / ConfigMap (API)
* Provisioner CR spec (API)
* NodeTemplate CR spec (API)
* Auto-detected values
* Computed values

The source of each parameter is documented in comments to `NodeBootstrapVariables` fields in [aksbootstrap.go](/pkg/providers/imagefamily/bootstrap/aksbootstrap.go). Note that some source assignments are still subject to change, and some are not implemented yet.

Of theses sources, Karpenter global configuration, Provisioner CR and NodeTemplate CR represent part of the external configuration surface / API, and should be treated accordingly.

The following sections describe each category in more detail.

<!-- TODO: address configuration refresh (e.g. ConfigMap reload) -->

### Hardcoded values

Hardcoded values are used for parameters in one of the following categories:
* unused (but required)
* unsupported by Karpenter (set to some sane default)
* static values that are either not expected to change, or are changing very slowly
* selected defaults

### Environment variables

Environment variables are used for global parameters that are needed for bootstrap and already required to be set for other reasons, such as Subscription ID - needed for Azure SDK.

### Karpenter global settings

Karpenter uses a ConfigMap with flexible structure for global settings. Part of this configuration is generic, and part is provider specific. See [Concepts/Settings/ConfigMap](https://karpenter.sh/preview/concepts/settings/#configmap) in Karpenter documentation for an overview. Note that this represents part of the external configuration surface / API, and should be treated accordingly.

For implementation see [pkg/apis/settings/settings.go](/pkg/apis/settings/settings.go). Note that these settings are made available anywhere in provider code via context.

### Provisioner CR spec

Some of the bootstrap-relevant configuration comes from the standard [Provisioner](https://karpenter.sh/preview/concepts/provisioners/) CR spec. The relevant fields are:

* `spec.taints`
* `spec.startupTaints`
* `spec.labels`
* `spec.annotations`
* `spec.requirements`
* `spec.kubeletConfiguration`

### NodeTemplate CR spec

Karpenter also supports provider-specific configuration via `NodeTemplate` custom resource. (See public docs for [`AWSNodeTemplate`](https://karpenter.sh/preview/concepts/nodetemplates/).)

Note that this represents part of the external configuration surface / API, and should be treated as such.

<!-- TODO: cover NodeTemplate details -->
<!-- TODO: add guidance on what belongs to settins vs NodeTemplate -->

### Auto-detected values

A small set of values that are (currently) required for bootstrap are auto-detected or computed:
* Kubernetes version is auto-detected from the cluster (though we will likely add a way to override it)
* CA bundle is obtained from TLS connection to kube-apiserver
* ClusterID is computed from the cluster endpoint FQDN (currently also customizable)

### Computed values

A small set of values is fully defined by, and is computed from, other variables. One example is `EnsureNoDupePromiscuousBridge`. (These are good candidates for node image to compute internally in the future.)

## AKS cluster configuration compatibility and drift

A subset of the bootstrap configuration is specific to a given AKS cluster. Cluster endpoint is an obvious input. Other examples include CNI plugin used in the cluster, bootstrap token (currently required to join the node to the cluster; expected to go away in the future), Kubernetes version, etc. Some of these values (ideally all but a handful) could be detected automatically, by interrogating the AKS or Kubernetes API. A subset could also change over the lifetime of the cluster, and ideally Karpenter should be able to detect and accommodate these changes.

For the sake of simplicity, current design does not fully address this. Instead it opts for sane defaults for some values, auto-detection for a few others, and expecting the remaining cluster-specific values to be explicitly specified in Karpenter global settings, without drift detection. This may be addressed in the future, by adding support for cluster configuration detection, refresh and propagation.

Note that nothing prevents external tooling from automatically configuring Karpenter global settings based on cluster configuration, prior to deployment. The provided `az-patch-skaffold` make target is an example of this. (Skaffold overrides propagate to Helm chart values which populate the global settings ConfigMap.)

<!-- TODO: cover Karpenter node configuration drift detection -->

<!-- TODO: cover resource configuration
## Resource configuration
### Virtual machine profile
 -->

## Relevant interfaces and wiring

This section describes some of the concepts and interfaces used in code that help abstract parts of the bootstrap process and wire everything together.

### Launch template provider

Launch Template is a useful concept that is used here to represent selected shared parameters needed to create VMs and associated resources. Launch template structure currently carries Custom Data, VM image ID, and desired Azure resource tags. Launch template is obtained and applied by instance provider at the time of VM creation.

Launch template provider is responsible for the generation of launch templates, using `Machine`, `NodeTemplate` and `InstanceType` as input. It delegates the generation of Custom Data to a particular image family, and the generation of VM image ID to an image provider. A component called Resolver helps pick the right image family and image provider, based on machine, node template and instance type.

### Image provider

Image provider is responsible for deciding on the right VM image to use, based on node template, instance type and image family.

### Image family

Image family abstracts everything related to a particular image family - such as Ubuntu, which is the only one currently supported. Its primary responsibility is the generation of Custom Data via selected implementation of the Bootstrapper interface.

### Bootstrapper interface

`Bootstrapper` interface defined in [providers/imagefamily/bootstrap/bootstrap.go](/pkg/providers/imagefamily/bootstrap/bootstrap.go) abstracts the process of generating bootstrap script (user data / custom data). It is implemented by the provider-specific bootstrapper, currently [AKS bootstrapper](/pkg/providers/imagefamily/bootstrap/aksbootstrap.go) is the only implementation. The interface is ultimately used by launch template to render the bootstrap script for passing into VM's custom data.

AKS bootstrapper is the one implementing the generation of Custom Data for the new "Node Bootstrap Variables" bootstrap process, described in the beginning of this document.

<!-- TODO: cover selected data flows: node labels, kubelet config, etc. -->
 