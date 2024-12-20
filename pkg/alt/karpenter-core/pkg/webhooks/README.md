This is copied from https://github.com/kubernetes-sigs/karpenter/blob/v1.0.4/pkg/webhooks/webhooks.go

Some modifications have been made to cater to the deployment model in AKS (multiple API servers).
Look for the sections that start with `// AKS customized` to understand the changes.
