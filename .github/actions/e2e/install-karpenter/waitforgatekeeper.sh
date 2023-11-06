#!/usr/bin/env bash

# wait for the namespace to exist
for i in $(seq 1 60);
do
namespaceoutput=$(kubectl get namespaces -A | grep gatekeeper-system)
if [ "$namespaceoutput" == "" ]; then
    echo "no namespace for gatekeeper-system yet";
    sleep 10;
else
    echo "namespace found"
    break;
fi
done

sleep 1

# wait for a pod to exist
for j in $(seq 1 60);
do
namespaceoutput=$(kubectl get po -n gatekeeper-system --no-headers)
if [ "$namespaceoutput" == "" ]; then
    echo "waiting on gatekeeper-system a pod to exist";
    sleep 1;
else
    echo "pod found"
    break;
fi
done

sleep 1

# waiting for pods to be ready
for k in $(seq 1 60);
do
namespaceoutput=$(kubectl get po -n gatekeeper-system --no-headers | grep -v Running | grep -v Completed)
if [ "$namespaceoutput" != "" ]; then
    echo "waiting on gatekeeper-system pods to be ready";
    sleep 1;
else
    echo "pods ready/completed"
    break;
fi
done
