# Overview

This docment is meant to service as a central list of practices, policies, and guidelines we've developed for the best approaches toward devopment of `karpenter-provider-azure`.

# Supported K8s Versions [Undecided]

Background, AKS has a list of supported k8s versions:
- [Supported Kubernetes versions in Azure Kubernetes Service (AKS)](https://learn.microsoft.com/en-us/azure/aks/supported-kubernetes-versions?tabs=azure-cli)

There's two options here I see:
1. We continuously leave the tests for older k8s version around, and keep them passing as we continue development unless there's a big reason to remove them. However, once a version goes out of support we wouldn't look at designing out new tests for it.
2. We drop any tests for older version once they go out of support. 

# Release Versioning [Undecided]
What's the release schedule? How frequently do we cut new minor version releases?

What qualifies a minor, and patch bump?

# Design docs
Anything under `/design` is locked at the time of design completion. While there might be minor updates, cleanups, or such that are needed after the fact, they are not our actual documentation, and will not be updated as any concepts within them fall out of date, accumulating rot.

# sigs.k8s.io/karpenter

## versioning

- We should never drift further than one minor version behind sigs.k8s.io/karpenter
- We can have commit version references in-between our releases, but for any `karpenter-provider-azure` release we will tie ourselves down to a whole version of `sigs.k8s.io/karpenter`.

# E2E testing:

New E2E test suites need to meet a minimum bar of 9/10 of their runs passing to be added to the testing matrix.