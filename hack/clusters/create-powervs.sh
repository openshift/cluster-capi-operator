#!/bin/bash

function printcolor {
  COLOR='\033[0;32m'
  NC='\033[0m'
  printf "${COLOR}$1${NC}\n"
}

printcolor "Getting required variables"
export CLUSTER_NAME=$(kubectl get infrastructure cluster -o jsonpath="{.status.infrastructureName}")
export IBMPOWERVS_SERVICE_INSTANCE_ID=$(kubectl get machineset.machine.openshift.io -n openshift-machine-api -o jsonpath="{.items[0].spec.template.spec.providerSpec.value.serviceInstance.id}")
export IBMPOWERVS_NETWORK_REGEX=$(kubectl get machineset.machine.openshift.io -n openshift-machine-api -o jsonpath="{.items[0].spec.template.spec.providerSpec.value.network.regex}")
export INFRASTRUCTURE_KIND=IBMPowerVSCluster

printcolor "Creating Power VS infrastructure cluster"
envsubst <hack/clusters/templates/powervs.yaml | kubectl apply -f -

printcolor "Creating core cluster"
envsubst <hack/clusters/templates/core.yaml | kubectl apply -f -

printcolor "Done"
