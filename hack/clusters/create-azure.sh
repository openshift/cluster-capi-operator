#!/bin/bash

function printcolor {
  COLOR='\033[0;32m'
  NC='\033[0m'
  printf "${COLOR}$1${NC}\n"
}

printcolor "Getting required variables"
export CLUSTER_NAME=$(kubectl get infrastructure cluster -o jsonpath="{.status.infrastructureName}")
export AZURE_REGION=$(kubectl get machineset.machine.openshift.io -n openshift-machine-api -o jsonpath="{.items[0].spec.template.spec.providerSpec.value.location}")
export INFRASTRUCTURE_KIND=AzureCluster

printcolor "Creating Azure infrastructure cluster"
envsubst <hack/clusters/templates/azure.yaml | kubectl apply -f -

printcolor "Creating core cluster"
envsubst <hack/clusters/templates/core.yaml | kubectl apply -f -

printcolor "Done"
