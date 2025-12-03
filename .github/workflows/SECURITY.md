# Security Guidelines for GitHub Workflows

Exposing workflows to external interactions introduces significant security risks. When workflows can be triggered or influenced by third parties, they become vulnerable to various attack vectors. These security concerns and mitigation strategies are documented in detail here: https://docs.github.com/en/actions/security-guides/security-hardening-for-github-actions

To minimize script injection vulnerabilities in the Karpenter workflows, we follow a consistent pattern: all values from GitHub context (such as `github.*`, `inputs.*`, `secrets.*`, etc.) must be assigned to environment variables before being used in shell or script steps within workflows or composite actions. An example of this pattern is shown below:

```yaml
- name: az set sub
  shell: bash
  env:
    SUBSCRIPTION_ID: ${{ secrets.E2E_SUBSCRIPTION_ID }}
  run: az account set --subscription "$SUBSCRIPTION_ID"
```

When writing or modifying GitHub workflows and composite actions, always follow this pattern to prevent script injection attacks and reduce the overall attack surface.
