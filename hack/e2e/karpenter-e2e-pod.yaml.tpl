apiVersion: v1
kind: Pod
metadata:
  name: ${ENV_POD_NAME}
  namespace: kube-system
  labels:
    app: karpenter-e2e
    suite: ${ENV_SUITE}
spec:
  restartPolicy: Never
  nodeSelector:
    kubernetes.azure.com/mode: system
  terminationGracePeriodSeconds: 30
  tolerations:
  - key: CriticalAddonsOnly
    operator: Exists
    effect: NoSchedule
  - key: node.kubernetes.io/not-ready
    operator: Exists
    effect: NoExecute
    tolerationSeconds: 300
  - key: node.kubernetes.io/unreachable
    operator: Exists
    effect: NoExecute
    tolerationSeconds: 300
  - key: node.kubernetes.io/memory-pressure
    operator: Exists
    effect: NoSchedule
  volumes:
  - name: kubeconfig
    secret:
      secretName: ${ENV_SECRET_NAME}
      defaultMode: 0420
  - name: kube-api-access
    projected:
      defaultMode: 0420
      sources:
      - serviceAccountToken:
          path: token
          expirationSeconds: 3607
      - configMap:
          name: kube-root-ca.crt
          items:
          - key: ca.crt
            path: ca.crt
      - downwardAPI:
          items:
          - fieldRef:
              apiVersion: v1
              fieldPath: metadata.namespace
            path: namespace
  containers:
  - name: karpenter-test
    image: mcr.microsoft.com/oss/go/microsoft/golang:1.24.2
    imagePullPolicy: IfNotPresent
    resources:
      requests:
        cpu: "4"
    command: ["/bin/bash","-c"]
    args:
    - |
      set -ex
      go install github.com/onsi/ginkgo/v2/ginkgo@v2.13.0

      git clone https://github.com/Azure/karpenter-provider-azure.git /workspace
      cd /workspace

      cd test && stdbuf -oL -eL go test \
        -p 1 \
        -count 1 \
        -timeout 5h \
        -v \
        ./suites/${ENV_SUITE}/... \
        --ginkgo.focus="" \
        --ginkgo.timeout=300m \
        --ginkgo.grace-period=3m \
        --ginkgo.vv
    env:
    - name: AZURE_CLUSTER_NAME
      value: ${ENV_AZURE_CLUSTER_NAME}
    - name: AZURE_RESOURCE_GROUP
      value: ${ENV_AZURE_RESOURCE_GROUP}
    - name: AZURE_LOCATION
      value: ${ENV_AZURE_LOCATION}
    - name: AZURE_ACR_NAME
      value: ${ENV_AZURE_ACR_NAME}
    - name: AZURE_SUBSCRIPTION_ID
      value: ${ENV_AZURE_SUBSCRIPTION_ID}
    - name: KUBECONFIG
      value: /kubeconfig/config
    volumeMounts:
    - name: kubeconfig
      mountPath: /kubeconfig
      readOnly: true
    - name: kube-api-access
      mountPath: /var/run/secrets/kubernetes.io/serviceaccount
      readOnly: true 