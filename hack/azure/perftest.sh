#!/bin/bash

# This deploys Provisioner requiring small instances (2 vCPU) and 'inflate' deployment with 1 cpu request, requiring VM per replica.
# It then scales the deployment up to the requested number of replicas (allocating the same number of VMs) and then scales it down.

# TODO: obtain cluster ID programmatically
CLUSTER_ID=63559813aff5f40001dfadb5
DASHBOARD=3052d470-b928-4e5e-bdbc-cc01e18ff318

set -euxo pipefail

if [ -z "$1" ]; then echo pass number of replicas; exit 1; fi
replicas="$1"

FMT='+%Y-%m-%dT%H-%M-%SZ'
START=$(date ${FMT})
STARTKUBECTL=$(date --iso-8601=seconds)

mkdir -p logs
exec > >(tee -i "logs/az-perftest-${START}-${replicas}.log")
exec 2>&1
logk="logs/az-perftest-${START}-${replicas}-karpenter.log"

# prep
kubectl apply -f examples/provisioner/general-purpose-azure-smallnodes.yaml 
kubectl apply -f examples/workloads/inflate.yaml

# scale up
date
kubectl scale --replicas="${replicas}" deployment/inflate
time kubectl rollout status deployment/inflate --watch --timeout=2h
date
ENDUP=$(date ${FMT})
echo Scale up: "https://dataexplorer.azure.com/dashboards/${DASHBOARD}?p-_startTime=${START}&p-_endTime=${ENDUP}&p-_cluster_id=${CLUSTER_ID}&p-_bin_size=v-20s"
ENDUPKUBECTL=$(date --iso-8601=seconds)
kubectl logs deployment/karpenter -n karpenter --since-time="${STARTKUBECTL}" > "${logk}"

# scale down
sleep 30
kubectl scale --replicas=0 deployment/inflate
date
kubectl delete --wait=false nodes -l karpenter.sh/provisioner-name
time kubectl wait --for=delete   nodes -l karpenter.sh/provisioner-name --timeout=30m
ENDDOWN=$(date ${FMT})
date

# review
kubectl logs deployment/karpenter -n karpenter --since-time="${ENDUPKUBECTL}" >> "${logk}"
az resource list -o table --tag=karpenter.sh_provisioner-name=default
# az resource wait --deleted --timeout 300 --tag=karpenter.sh_provisioner-name=default - can't wait on tags :(

# Cluster Autoscaler dashboard links - handy for some metrics
echo Scale up:   "https://dataexplorer.azure.com/dashboards/${DASHBOARD}?p-_startTime=${START}&p-_endTime=${ENDUP}&p-_cluster_id=${CLUSTER_ID}&p-_bin_size=v-20s"
echo Scale down: "https://dataexplorer.azure.com/dashboards/${DASHBOARD}?p-_startTime=${ENDUP}&p-_endTime=${ENDDOWN}&p-_cluster_id=${CLUSTER_ID}&p-_bin_size=v-20s"
