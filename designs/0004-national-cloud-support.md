# Supporting non-public clouds

## Overview

This document discusses how Karpenter will support non-public clouds.

There are two classes of non-public cloud

1. Known clouds (Mooncake, Fairfax, etc).
2. Nonstandard/Azure-Stack like clouds.

## Current state

* Karpenter accepts an [`ARM_CLOUD` env variable](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/auth/config.go#L84) but it is not used consistently.
* We also accept other env variables for `LOCATION`, `ARM_TENANT_ID`, `ARM_SUBSCRIPTION_ID`, and authentication details.

## Context

Before we can answer what we should do, we need to understand how things work today for other tools like Cluster Autoscaler and CloudProvider.

Both of these tools use [the CloudProvider clients](https://github.com/kubernetes-sigs/cloud-provider-azure/tree/master/pkg/azclient) to configure
the active cloud.

Two files are used by CloudProvider to configure the two different types of clouds mentioned above:

* [Cloud provider configuration /azure/config/azure.json](https://cloud-provider-azure.sigs.k8s.io/install/configs/)
* [environment.json](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L106).
  Note that the structure is [a copy of the track1 SDK structure](https://github.com/Azure/go-autorest/blob/main/autorest/azure/environments.go). This is written
  by AgentBaker to [/etc/kubernetes/AzureStackCloud.json](https://github.com/Azure/AgentBaker/blob/d786bf15845160db41fcb92f45928f290439582c/parts/linux/cloud-init/artifacts/cse_config.sh#L232).
  The `/etc/kubernetes/AzureStackCloud.json` is then passed to CloudProvider as the value of `AZURE_ENVIRONMENT_FILEPATH` ([code](https://github.com/Azure/AgentBaker/blob/d786bf15845160db41fcb92f45928f290439582c/aks-node-controller/parser/parser.go#L15)).

## Topics

We are not currently using the CloudProvider clients, while CAS (and probably some other tools) are. So topics for discussion are:

1. Should we use the CloudProvider clients to help us access the Cloud details?
2. How should we pass the cloud details to Karpenter?

### Should we use the CloudProvider clients?

Location: https://github.com/kubernetes-sigs/cloud-provider-azure/tree/master/pkg/azclient

#### Cloud Environment

The CloudProvider Cloud Environment hierarchy [seems to be](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L133) (later wins):
- Read [ARMClientConfig](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/arm_conf.go#L31) from [Cloud provider configuration /azure/config/azure.json](https://cloud-provider-azure.sigs.k8s.io/install/configs/).
- [Read from IMDS /endpoints API](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L42)
- [AZURE_ENVIRONMENT_FILEPATH](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L106).

Note that the way that cloud provider does this will actually _not_ work for Karpenter out of the box because they do not map the entire Track1 SDK shape into the track 2 shape, they _just_ map the [3 important fields](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L121). We will also need the graph endpoint which they are not mapping.
```
	if err = json.Unmarshal(content, env); err != nil {
		return err
	}
	if len(env.ResourceManagerEndpoint) > 0 && len(env.TokenAudience) > 0 {
		config.Services[cloud.ResourceManager] = cloud.ServiceConfiguration{
			Endpoint: env.ResourceManagerEndpoint,
			Audience: env.TokenAudience,
		}
	}
	if len(env.ActiveDirectoryEndpoint) > 0 {
		config.ActiveDirectoryAuthorityHost = env.ActiveDirectoryEndpoint
	}
```

Other tools may do something different, for example CAS doesn't use `GetAzureCloudConfigAndEnvConfig`, it instead [builds the configuration from `/azure/config/azure.json`, overridden with env variables](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/azure/azure_config.go#L175).

#### Authentication

Authentication is also reading from [/azure/config/azure.json](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/auth_conf.go#L27)

#### Decision

**Pros**

* Existing helpers to deserialize well-known formats like `AZURE_ENVIRONMENT_FILEPATH` (originally from track1), `/azure/config/azure.json`.
* Client [ratelimit support](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/policy/ratelimit/ratelimit.go#L46) - configured from `/azure/config/azure.json` by default, but disabled by default.

**Cons**

* Non-idiomatic [factory interface](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/factory.go) for use with GoMock.
* Cloud environment setup is honestly a big mess. A lot of ways to configure things, various inputs from multiple files that can be ignored or overwritten (or partially ignored) in certain cases.
  Doesn't actually map all of the values we would need now so we would need to contribute upstream to get graph endpoint mapped to track2 configuration.
* Doesn't expose the full feature set of the underlying SDK - for example [no Pagination on VM list](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/virtualmachineclient/interface.go#L36).
  We may not need these features but if we ever do, additional overhead to add them.
* We already do authentication differently (via env variables). Passing `--cloud-config` and taking `/azure/config/azure.json` would be confusing because
  now we need to answer which wins, the auth details in that file or the auth details we accept already via the environment?
* It would likely be a significant amount of work to change to the CloudProvider clients. The benefits don't seem worth the engineering investment.

**Conclusion:** No, we should continue using the raw clients. We _may_ want to refer to some helpers from CloudProvider for file structure and/or utilities like rate-limiting, but those can be plugged directly into the raw Go SDK without
needing to use the full CloudProvider client set.

### How should we pass the cloud details to Karpenter?

The main disadvantage of using `/azure/config/azure.json` is that it is a "bag of everything". Introducing it into
Karpenter configuration might cause confusion about exactly which fields are we using from it.

Questions like these become harder to answer when we consume a complex file like `azure.json` but don't use all of it:
* Do we honor the throttling configuration?
* Can users configure throttling separately for Karpenter vs CloudProvider even though they're both reading from the same file?
* Do we honor the credential/identity details?
* When `azure.json` is specified alongside environment credentials, which wins?

On the other hand, `/etc/kubernetes/AzureStackCloud.json`, often passed to the `AZURE_ENVIRONMENT_FILEPATH` env variable, is singularly focused on describing the cloud environment, which is what we want and wouldn't add confusion.
It has the advantage of being on the nodes already in clouds that require it, saving users from needing to configure the endpoints themselves in the Karpenter chart in self-hosted scenarios.
Note that this is _not_ in public and some other well-known clouds, so it cannot be relied upon to be the sole environment configuration source.

We also already use the `environment.json` format from CloudProvider in a few locations, such as in [azure_client.go](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/providers/instance/azure_client.go#L108). There are a couple usages of `azclient.EnvironmentFromName` and `*azclient.Environment` (the struct returned from `azclient.EnvironmentFromName` based on the `/etc/kubernetes/AzureStackCloud.json` file) in the provider already. Unless we want to remove `sigs.k8s.io/cloud-provider-azure/pkg/azclient` as a dependency, it seems like a good idea to:
1. Continue to use `azclient.EnvironmentFromName` to read `*azclient.Environment` from the Cloud Provider environment configuration for "known clouds".
2. Define a mapping from `*azclient.Environment` (track 1 format) to `cloud.Configuration` (track 2 format).
3. Define our own helper to read `*azclient.Environment` from file. We should not rely on [OverrideAzureCloudConfigFromEnv](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L106) because:
  * The logic there would need to be changed to support the graph URI and any other URIs Karpenter ends up needing. This is not hard to upstream but it's also a small amount of code to
    duplicate and   being able to evolve separately from CloudProvider is probably worth it here.
  * It requires the `ARM_CLOUD` set to `AZURESTACKCLOUD`. We could also require this for consistency with other tools (CAS) but from a purely technical standpoint it's not correct.
  * As written, `OverrideAzureCloudConfigFromEnv` allows for partial overrides which seems risky at best.

#### Decision

**For known clouds (AzureChina, AzureGovernment, etc)**: Use the existing `ARM_CLOUD` env variable with well-known cloud names and CloudProvider `azclient.EnvironmentFromName`.

**For Nonstandard/Azure-Stack like clouds:**: New env variable and/or cmdline option for `AZURE_ENVIRONMENT_FILEPATH`, new method to read it, using the CloudProvider `*azclient.Environment`
but not sharing the CloudProvider "override" logic
