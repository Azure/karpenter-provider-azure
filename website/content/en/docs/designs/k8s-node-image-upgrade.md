---
title: "K8s node image upgrade"
linkTitle: "K8s node image upgrade"
weight: 40
---

## Motivation

There is no current support for upgrading the k8s version, and/or node image version for karpenter managed nodes.

Current behavior:
- K8s version is simply pulled from the API server at node creation and if an API server version upgrade happens any older nodes on previous k8s versions are left running on their given k8s version
- If the imageID is specified for a NodeTemplate that will be used. Otherwise, the newest node image version is pulled on node creation, and is then cached for 3 days. There is no upgrade process for node image versions once the node is created

## Goals

The goal is to allow for us to control both k8s, and node image upgrade of karpenter controlled nodes, with an automated upgrade option.

## Non-goals

- Any other reimage scenario

## Metrics for Success

-   Karpenter adoption, with high QoS, and low number of customer CRIs

# Solution Overview

When considering options, there have been multiple factors important and considered to us:

- Requirements: <br>

    - Committed:
        - automated upgrading of k8s [AKS autoupgader], and node image version [Karpenter drift]
        - Maintenance windows support for k8s autoupgrading
        - Resource Thresholds [Exists]

    - Deferred:
        - Maintenance windows support for node image version upgrade
            - Deferred as its a risk to preview
            - Requires a whole new design space for addressing this
            - Changed to post preview support/stretch goal, as its a far easier design space once we have an agentpool representation
        - Max Surge [Deferring as it'd require a `karpenter-core` fork, and they already have a design on the way for delivering this within the main line]
            - Max surge doesn't currently exist as a settable condition within karpetner core for drift.
            - Drift's current batching logic is to deprovision and remove all drifted nodes that don't have pods running on them in one big batch. After that it goes through one by one deprovisioning one candidate at a time, where it will spin up any needed replacement nodes before removing the older one.
            - If we wanted to implement it for preview, I don't think it would be a hard section of code to add. There is actually a design in the works for `kaprenter-core`to add batching by node counts, but my concern is that it won't be available before our preview timeline.
            - I think if we do implement max surge we should have it only be pod based, as its a better proxy for resources than node count. Note: it may have to round up to the nearest integer to have a minimum of 1 node [0 special cased to 0], which doesn't feel great as it'd be violating the actual setting as a threshold.
            - I think the following design within `karpenter-core` will be the best working solution for us here: <br>
            https://github.com/aws/karpenter-core/pull/516#pullrequestreview-1636663551 <br>
            Although, it's node based which I don't like that much. That said if node based becomes an issue for other customers I expect `karpenter-core` to provide other  options for configurability. For preview though, the timeline of it is unknown, and expect it may not be available in time.
        - Soak [Requires a more complicated work around, or forking `karpenter-core`]
            - Soak currently doesn't exist for karpenter
            - Currently after a successful drift it will requeue after 1 millisecond
            - I don't think implementing this would be too hard, if added within `karpenter-core`, as we could simply add a wait after the drifting of a batch is complete if its not he last batch. However, it would require forking `karpenter-core`, so plan to defer as we'd like to avoid that.
            - The plan is to defer and push for/contribute to `karpenter-core` design/implementation to get this behavioral control within `karpenter-core` directly.

    - Removed:
        - Timeout [Removed as a requirement since karpenter retries forever, and so this doesn’t make much sense]
            - Note: we should ensure we have good backoffs though for failure.

The solutions are based on the following assumptions:
1. No new AKS API representation of provisioners or their nodes
    - AKS API representation will possibly come down the line.
2. ~~We want k8s, and node image upgrade to follow the same API flow~~
3. We are using karpenter drift to do the actual upgrades
4. Customers need to have an override to disable automated node image upgrades if desired
    - There is one new field on the NodeTemplate to support this:
        - Node Image: nodetemplates.imageVersion
            - Note: imageVersion is new to allow the customer to lock all nodes associated with a NodeTemplate to an associated imageVersion, and prevent automated upgrading. We don't want to use the current imageId, as we don't want to force them to all be on the same imageId.

## Option A: Managed

Upgrade Flow:
- node image:
    - Defaults: to node image automated upgrade behavior [Not using AKS autoupgrader. Karpenter detects the new versions itself]
    - Allows overriding to a manual upgrade process by setting the imageVersion on the nodetemplates, and locking associated nodes to the given imageVersion, until either the customer manually upgrades by setting a new imageVersion, or removed to migrate back to autoupgrade 
- k8s:
    - locked to always update and upgrade with the MC/Control plane version, with no override option

Validation:
- node image:
    - no committed validation for customer specified node image
    - stretch goal:
        - op1: via a webhook
        - op2 [preferred]: accepting invalid input, but displaying within status the invalid and last valid version
- k8s:
    - validated via AKS RP, since its triggered based on AKS API call to upgrade MC k8s version

Defaulting:
- karpenter is currently already setup to handle the defaulting of both k8s/node image as presented in this design option

Pros/Cons:

-   Pros
    - Follows the flow of karpenter quite well
    - Customers don't have to worry about upgrades of k8s, or node image unless they want to.
    - Enabling autoupgrader on a k8s version setting will make the cluster fully managed for upgrades.
    - No need for an agentpool representation, or any new AKS API representation
    - Get to use the AKS RP validation on k8s version
    - Defaults customers to automated upgrades of node image versions which will help them get security updates.
    - Allows downgrading node image version?

-   Cons
    - Customer has to specify versions themselves when overriding. i.e. finding the node image version they want
    - Allows downgrading of node image version?
    - k8s, and node image have different upgrade flows

## Option B: Manual [Upgrade via CRs]

Upgrade Flow:
- node image: 
    - Default: CR gets locked to the latest node image version at the time of creation.
    - Upgrade: Customers can manually trigger upgrades by specifying a node image version, and/or triggering an empty field to retrigger the default fill the upgrade to latest
- k8s:
    - Default: CR gets locked to the on create MC/API server version.
    - Upgrade: Customers can manually trigger upgrades by specifying a new k8s version.

Validation/Defaulting:
- Validating webhook: handles k8s, and node image version validation
    - Requires a supported k8s/node image version, and k8s version to be <= MC/Control Plane version
    - Note: we are missing validation for the MC/Control Plane not getting too far ahead of the Provisioner, which we have similar code in RP for with agentpools.
    - Also, missing other potentially re-usable validation/restrictions like jumping too many k8s versions at once?
- Defaulting webhook: handles defaulting k8s version to API-server version, and node image to latest. 
    - Note: this would be a path for customers to upgrade k8s to MC/Control Plane version, and node image to latest, if they sent an empty field that would be defaulted.
- The other option for validation, and defaulting is skipping it entirely for preview
    - Note: We might end up not having a choice within this with how the CCP webhooks work as we might not be able to inject this validation logic we want.
        - If that is the case we still could add a reconciler for defaulting the k8s/node image versions though. Alex Leites' idea

Pros/Cons:

-   Pros
    - All done directly within karpenter.
    - k8s and node image version upgrade both follow the same API flow through CRs
    - Allows downgrading of k8s, and node image version?

-   Cons
    - harder, and/or less validation
    - No automated upgrades, until we have agentpool representation
    - Customer has to specify versions themselves. i.e. finding the node image version, k8s version they want, etc, unless defaulting is supported.
    - Allows downgrading of k8s, and node image version?

## Choice

We are going with Option A (Managed) for a couple reasons:
- NAP clusters are meant to be a more managed/automated offering. Due to this it makes sense for the clusters to have automated security node image upgrades by default.
    - Supporting automated upgrading is a committed requirement
- AKS RP is already going to default create clusters with node image autoupgrade equivalent on as well, so this is aligning with the existing AKS RP design as a whole.
- This option still allows customers to override the higher frequency node image upgrades if needed, but makes it as managed by default
    - The plan is to allow for more configurability if/once we get to a "nodepool" concept within the AKS API

# High Level Design

Drift:
1. Karpenter will detect associated nodes to the NodeTemplate have drifted on k8s, or node image version and an upgrade is required
2. The nodes will be replaced, until all nodes associated with the NodeTemplate are on the correct k8s, and/or node image versions

node image:
- Automated:
    1. A new node image version is released
    2. karpenter will detect the new node image version within ~3 days [caching length for latest node image version]
    3. `Drift`
- Overridden Manual:
    1. customer sets a new node image version within the NodeTemplate CR
    2. `Drift`

k8s:
1. Customer triggers a k8s upgrade of the MC
2. API server is upgraded
    - Note: should there be a delay here?
3. `Drift`

# Detailed Design

Modifying NodeTemplate [API]:
- **NEW**: nodetemplates.imageVersion

Features:
- **NEW**: Drift enabled for all karpenter clusters

## Api and Data Model Change

Modifying NodeTemplate [API]:
- **NEW**: nodetemplates.imageVersion

**Open Question**:

1. Q: Do we fork `karpenter-core`?
    - Is `Max Surge` a hard requirement for preview, and will there be a satisfying `karpenter-core` option by preview?
    - A: No, `Max Surge`, and `Soak` have been deferred as to not require a fork
2. Q: Do we do validation on node image version for preview?
    - A: Moved to a stretch goal
3. Will enabling drift for all clusters get as any behavior we don't want?
4. How do we do monitoring? I believe Bryce was setting up monitoring for karpenter?
5. What solution will we have for node bootstrapping breaking on MC k8s version upgrade?
    - Node bootstrapping breaks on k8s version upgrades of the API server. This might get fixed by secure bootstrapping if available before preview, but if its not than we need some other sort of work around. We have options that should be doable, but need to keep an eye on the solution space here.

### **Testing Plan**

- E2E tests:
    - Drift feature works in general
    - Should not drift if feature is disabled
    - Drift on k8s
    - Drift on node image
- Unit tests:
    - Any new core functionally should have supporting tests

### **Perf & Scale Consideration**

If Max Surge remains at 1, this will be an issue for scale, as cluster upgrades will then take a long time to complete.

I would like if Max Surge is controllable, and has a reasonable baseline default of 10%, or 30% of whatever resources its based on (pods, nodes, etc) [10-30% are magic numbers based on experience with RP upgrades], but this is dependent upon if we fork `karpenter-core`, and/or what design options are available to us.

### **Monitoring**

TBD - Need to talk with Bryce

### **Deployment**

General deployment will be bundled with the karpenter deployment.

If a validation webhook is done, my understanding the CCP webhooks run as a sidecar for the API server. Unsure how that deployment might be different

## Security and Privacy

### Authentication and Authorization

Are there any concerns around customers having an access to an upgrade path through the kubernetes API as opposed to the AKS API?

Would customers ability to define permissions for their teams be restricted in ways they might not be satisfied with?

### Auditing

Unsure. Does anything have to be done here?

### Privacy

Don't think there's anything special that has to be done here.

## Observability Considerations

Dependent upon the open question on monitoring, and how we can do about monitoring upgrades, and their failures.

From that we should define metrics, accepted thresholds and alerts based on the monitoring data we have.

## Compatibility Consideration

Main potential issue with compatibility would be if we add a validation webhook and if they are non-functional with karpenter deployed within the CCP

## COGS Impacts

I don't think so. The only real change here would be using up some of our quota for API calls as far as I can tell.

If a validation webhook is added, its good to consider the resources it'd be allotted and using

## Rollout Plan

This will rollout with the karpenter preview launch.

## Migration

This is more a question of the migration plan for old clusters to karpenter in general. I don't think there's anything specific for upgrade here, unless we'd need to ensure they're on the MC/API server's k8s version, and possibly fill in the node image version on the CR if we weren't wanting to put them on autoupgrade for node image versions.

## Supportability Considerations

I think this will mostly just be whatever monitoring, alerting, and TSGs we end up getting out of the open question around monitoring.

# Meeting Notes[^6]

**9-22:** <br>
Updated design heavily from Option B to Option A. 

The original design had gone under the assumed requirement that we weren't providing any autoupgrade solution by default, and so something along those lines would come around when the AKS API was introduced. This assumption had been a misunderstanding, which made Option A available and the new preferable option. 

This doc has been adjusted accordingly

**Offline chats:** <br>
Long discussions around the potential support for maintenance windows for node image version automated upgrading. There were a few different control patterns:
1. autoupgrader handles the detection/triggering
    - Already had the handling for respecting maintenance windows
    - Would need kubeclient access, or new API access to detect/trigger upgrades on karpenter CRs
2. Karpenter controlled
    - Would need to have a way of either fetching  the maintenance windows, or having them pushed to it
    - Would block customer’s non-automated upgrades as well.

# References

- Mega Issue: Deprovisioning Controls <br>
https://github.com/aws/karpenter/issues/1738

- Drift Max Surge design within `karpenter-core`: <br>
https://github.com/aws/karpenter-core/pull/516#pullrequestreview-1636663551

- POC PR for drift supporting k8s version upgrade: <br>
https://github.com/Azure/karpenter/pull/262

---

From template:

[^1]: Coordinator is in charge of helping the author to find right
    person to answer technical questions (e.g if I do this, is there any
    big concern from RP's perspective?), as well as finding the right
    person to approve the design from the point of their domain. The
    coordinator is also in charge of setting up review meeting and
    making sure all the right stakeholders are involved in the design
    discussion and review.

[^3]: This list is dynamic. The Sponsor, Airlock, Security rows should
    always be there, but the remaining rows are as needed. The
    Coordinator's job is to make sure all required rows are filled.

[^4]: Sponsor is the EM who allocates resources to accomplish
    engineering work. For cross team collaboration, this needs to be
    approved by all EM's of involved team.

[^5]: For each SIG, the SIG lead is in charge of either approving the
    design doc by carefully reviewing it or assigning a tech leads that
    are domain expert in that SIG to review and approve this doc

[^6]: Q&A style meeting notes from desgin review meeting to capture
    todos
