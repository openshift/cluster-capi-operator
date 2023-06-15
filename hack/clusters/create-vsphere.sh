#!/bin/bash

function printcolor {
  COLOR='\033[0;32m'
  NC='\033[0m'
  printf "${COLOR}$1${NC}\n"
}

printcolor "Getting required variables"
export CLUSTER_NAME=$(kubectl get infrastructure cluster -o jsonpath="{.status.infrastructureName}")
export INFRASTRUCTURE_KIND=VSphereCluster

printcolor "Creating VSphere infrastructure cluster"
envsubst <hack/clusters/templates/vsphere.yaml | kubectl apply -f -

printcolor "Creating core cluster"
envsubst <hack/clusters/templates/core.yaml | kubectl apply -f -

printcolor "Done"
