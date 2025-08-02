# Supporting non-public clouds

## Overview

This document discusses how Karpenter will support non-public clouds.

There are two classes of non-public cloud

1. Known clouds (Mooncake, Fairfax, etc).
2. Nonstandard/Azure-Stack like clouds.

## Current state

* Karpenter accepts an [`ARM_CLOUD` env variable](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/auth/config.go#L84) but it is not used.
* We also accept other env variables for `LOCATION`, `ARM_TENANT_ID`, `ARM_SUBSCRIPTION_ID`, and authentication details.

## Context

Before we can answer what we should do, we need to understand how things work today for other tools like Cluster Autoscaler and CloudProvider.

Both of these tools use [the CloudProvider clients](https://github.com/kubernetes-sigs/cloud-provider-azure/tree/master/pkg/azclient) to configure
the active cloud.

Two files are used by CloudProvider to configure the two different types of clouds mentioned above:

* [Cloud provider configuration /azure/config/azure.json](https://cloud-provider-azure.sigs.k8s.io/install/configs/)
* [/AzureStackCloud/environment.json](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L106) as read from `AZURE_ENVIRONMENT_FILEPATH` env variable.
  Note that the structure is [a copy of the track1 SDK structure](https://github.com/Azure/go-autorest/blob/main/autorest/azure/environments.go).


## Topics

We are not currently using the CloudProvider clients, while CAS (and probably some other tools) are. So topics for discussion are:

1. Should we use the CloudProvider clients to help us access the Cloud details?
2. How should we pass the cloud details to Karpenter?

### Should we use the CloudProvider clients?

Location: https://github.com/kubernetes-sigs/cloud-provider-azure/tree/master/pkg/azclient

#### Cloud Environment

The CloudProvider Cloud Environment hierarchy [seems to be](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L133) (later wins):
- [ARMClientConfig](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/arm_conf.go#L31) read from [Cloud provider configuration /azure/config/azure.json](https://cloud-provider-azure.sigs.k8s.io/install/configs/).
- [Read from IMDS /endpoints API](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L42)
- [AZURE_ENVIRONMENT_FILEPATH](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/cloud.go#L106). Note that the structure is [a copy of the track1 SDK structure](https://github.com/Azure/go-autorest/blob/main/autorest/azure/environments.go)

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
* Doesn't expose the full featureset of the underlying SDK - for example [no Pagination on VM list](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/azclient/virtualmachineclient/interface.go#L36).
  We may not need these features but if we ever do, additional overhead to add them.
* We already do authentication differently (via env variables), passing `--cloud-config` and taking `/azure/config/azure.json` would be confusing because now we need to answer which wins, the auth details in that file or the
  auth details we accept already via the environment?
* It would likely be a significant amount of work to change to the CloudProvider clients. The benefits don't seem worth the engineering investment.

**Conclusion:** No, we should continue using the raw clients. We _may_ want to refer to some helpers from CloudProvider for file structure and/or utilities like rate-limiting, but those can be plugged directly into the raw Go SDK without
needing to use the full CloudProvider client set.

### How should we pass the cloud details to Karpenter?

The main disadvantage of using `/azure/config/azure.json` is that it is a "bag of everything". Introducing it into
Karpenter configuration might cause confusion about exactly which fields are we using from it. Do we honor the throttling details? Can users configure client throttling separately for Karpenter vs CloudProvider?

On the other hand, `/AzureStackCloud/environment.json`, often passed to the `AZURE_ENVIRONMENT_FILEPATH` env variable, is singularly focused on describing the cloud environment, which is what we want and wouldn't add confusion.
It has the advantage of being on the nodes already in clouds that require it, saving users from needing to configure the endpoints themselves in the Karpenter chart in selfhosted scenarios.

#### Decision

**For known clouds (Mooncake, Fairfax, etc)**: Use the existing `ARM_CLOUD` env variable with well-known cloud names.
**For Nonstandard/Azure-Stack like clouds:**: New env variable and/or cmdline option for `AZURE_ENVIRONMENT_FILEPATH`.
