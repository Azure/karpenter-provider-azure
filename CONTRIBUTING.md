# Contributing

## Overview

This project welcomes contributions, issue complaints, and suggestions. Maintainer responses may be best-effort and subject to internal prioritization. We appreciate your patience.

In many cases, we also recommend checking out or filing an [issue](https://github.com/Azure/karpenter-provider-azure/issues) to distinguish the problem/goal from the solution/implementation, to allow the flexibility of solutions toward a common goal, and to make the impact of the changes clear to the non-contributor users.

## Contribution Agreement

Most contributions require you to agree to a Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions provided by the bot. You will only need to do this once across all repositories using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

## Dev Environment

> A [GitHub Codespaces]((https://github.com/features/codespaces)) development flow is described below, which you can use to test Karpenter functionality on your own cluster, and to aid rapid development of this project.

1. **Install VSCode**: Go [here](https://code.visualstudio.com/download) to download VSCode for your platform. After installation, in your VSCode app install the "GitHub Codespaces" Extension. See [here](https://code.visualstudio.com/docs/remote/codespaces) for more information about this extension.

2. **Create Codespace** (~2min): In browser, click Code / "Create a codespace on main" (for better experience customize to use 4cores/8GB), wait for codespace to be created. It is created with everything needed for development (Go, Azure CLI, kubectl, skaffold, useful plugins, etc.) Now you can open up the Codespace in VSCode: Click on Codespaces in the lower left corner in the browser status bar region, choose "Open in VSCode Desktop". (Pretty much everything except for `az login` and some `az role assignment` works in browser; but VSCode is a better experience anyway.)

3. **Provision cluster, build and deploy Karpenter** (~5min): Set `AZURE_SUBSCRIPTION_ID` to your subscription (and customize region in `Makefile-az.mk` if desired). Then at the VSCode command line run `make az-all`. This logs into Azure (follow the prompts), provisions AKS and ACR (using resource group `$CODESPACE_NAME`, so everything is unique / scoped to codespace), builds and deploys Karpenter, deploys sample `default` Provisioner and `inflate` Deployment workload.

4. Manually scale the `inflate` Deployment workload, watch Karpenter controller log and Nodes in the cluster. Example of manually scaling up to 3 pods:
```
kubectl scale deployments/inflate --replicas=3
```

### Debugging

To debug Karpenter in-cluster, use `make az-debug`, wait for it to deploy, and attach from VSCode using Start Debugging (F5). After that you should be able to set breakpoints, examine variables, single step, etc. (Behind the scenes, besides building and deploying Karpenter, `skaffold debug` automatically and transparently applies the necessary flags during build, instruments the deployment with Delve, adjusts health probe timeouts - to allow for delays introduced by breakpoints, sets up port-forwarding, etc.; more on how this works is [here](https://skaffold.dev/docs/workflows/debug/).

Once done, you can delete all infra with `make az-rmrg` (it deletes the resource group), and can delete the codespace (though it will be automatically suspended when not used, and deleted after 30 days.)

### Developer notes

- If you see platform architecture error during `skaffold debug`, adjust (or comment out) `--platform` argument.
- If you are not able to set/hit breakpoints, it could be an issue with source paths mapping; see comments in debug launch configuration (`launch.json`)
- When writing `az` CLI commands in scripts or documentation, always specify an explicit `--output` format (`--output json`, `--output tsv`, `--output table`, `--output none`) on any command whose output is consumed programmatically. Users may have changed their default output format via `az configure`, which breaks scripts that assume JSON output. Use `--output tsv` with `--query` for single-value extraction; use `--output json` when piping to `jq`.

### FAQs

Q: I was able to trigger Karpenter to execute scaling up nodes as expected, using my own customized deployment of pods. However, scaling down was not handled automatically when I removed the deployment. The two new nodes created by Karpenter were left around. What is going on?

A: Additional system workloads (such as metrics server) can get scheduled on the new nodes, preventing Karpenter from removing them. Note that you can always use `kubectl delete node <node>`, which will have Karpenter drain the node and terminate the instance from cloud provider.

Q: When running some of the tests locally, the environment failed to start. How can I resolve this?

A: Oftentimes, especially for pre-existing tests, running `make toolchain` will fix this. This target will ensure that you have the correct versions of binaries installed.

Q: What is `skaffold.yaml`?

A: [skaffold.yaml](https://github.com/Azure/karpenter/blob/main/skaffold.yaml) is the configuration file for deploying Karpenter locally via [skaffold](https://skaffold.dev/docs/).

Q: Why are its modifications showing up locally even though I didn't change it?

A: To deploy/test locally, we run `make az-all`. This make command is composed of many different steps needed to deploy. One step patches your local skaffold.yaml file by updating certain variables based on env vars defined in the `Makefile-az.mk`.

## Solution Design, Implementation, and Testing

Refer to [AGENTS.md](AGENTS.md) for guidelines. It is designed to be understandable by humans and agents alike.

## Pull Request Discussions and Submission

Supplementary materials of high quality promote clarity, which makes the integration and long-term maintenance more correct and efficient. We ask that the guidelines below be followed.

In addition, follow the relevant guidelines in PR descriptions. Consider them more up-to-date if they conflict with the guidance below (and call them out).

- Consider filing an [issue](https://github.com/Azure/karpenter-provider-azure/issues) to distinguish the problem from the solution (your PRs). This creates flexibility in solutions toward an intended goal and clarity for non-contributors.
- Consider splitting a large PR into multiple, relatively independent PRs, potentially with the help of [GitHub stacked pull requests](https://docs.github.com/en/pull-requests/get-started/about-stacked-prs).
- Do estimate the size of design changes. Consider adding to [design docs](designs/) instead of PR description for large ones.
- Otherwise, please follow the PR description format. **PRs with a severely malformed format may be deprioritized or ignored.**
- All temporary changes and technical loans must be documented in some way or have an issue filed.
- Every PR, regardless of AI uses, must be self-reviewed by the human author before being considered ready for review by the maintainers.

## AI Uses

See [Kubernetes: Open source maintainership in the age of AI](https://kubernetes.io/blog/2026/06/26/open-source-maintainership-in-the-age-of-ai/) as a starting point.

We see AI as a tool for us humans for improving delivery with the same effort. Commitment to quality and accountability for impact remain with the human author, especially given the current quality gaps between AI and human contributions.

In addition to key considerations listed below, follow the relevant guidelines in PR descriptions. Consider them more up-to-date if they conflict with the guidance below (and call them out).

### Note: PR Review Bottleneck

In several cases, the bottleneck in software development is leaning away from code changes towards PR reviews. The accountability to ensure the changes are delivered correctly makes it hard. While different tools are being developed to ease this bottleneck (e.g., a comprehensive testing framework that gives confidence when passing), **PRs with high value and low review effort required will get prioritized**, and it is encouraged that these properties be demonstrated by the author.

### Distinguishing the Problem, the Design, and the Implementation

- If you see the problem, but believe you or your AI may not make a meaningful contribution, please open an [issue](https://github.com/Azure/karpenter-provider-azure/issues). You may still publish a PR with a confidence level denoted.
- On any PR, the human author must be aware of, understand, and be accountable for the underlying design or assumption changes.
  - One option to demonstrate that understanding is to write the PR description, design docs, or in-code comments yourself.
  - We do not recommend producing a large PR entirely with AI, especially without a demonstration of your understanding.

### Development

- Declare how AI is being used during development in the PR description.
- Given others are also equipped with some kind of AI, whether that's Artificial Intelligence or Actual Intelligence, ask yourself "what difference do you or your AI bring, and how can you demonstrate it?"
  - Examples of the values humans can contribute well:
    - Creativity
      - E.g., feature ideas, design directions, test planning
    - Attention to detail
      - E.g., problem/requirement clarification, guiding and verifying AI works, PR/design reviews, gaps clarification
    - Context and diversity in experience
      - E.g., obscure design assumptions, cross-module assumptions, confidence evaluation and drawing attention to the part that matters
    - Cognitive context switching to work on this specific task
      - E.g., even for smaller PRs, it consumes a person's attention to command their AI to create one
    - Accountability and attention
      - E.g., problem awareness and prioritization, or that you may care about this change and will monitor the results in the long run
  - Examples of what your AI can contribute well against other AI:
    - Customized context or instructions
    - Model and token cost

### Documentation and Communication

"Direct AI writing" means using AI to generate a block of descriptive English text in one go. Using AI to proofread, translate, enhance/rewrite specific parts does not fall under this umbrella.

- Direct AI writing is discouraged due to the gaps in language quality. Good harnesses (e.g., [wait-what skill](https://github.com/mattpocock/skills/blob/main/skills/productivity/wait-what/SKILL.md)) can be used to mitigate this to a certain degree.
- Every piece of text resulting from direct AI writing (e.g., the PR itself, the comment) must indicate its AI-written nature.
- Direct AI writing with more than 150 words **is not allowed**, except for appendices, data analysis, or supplementary information.
