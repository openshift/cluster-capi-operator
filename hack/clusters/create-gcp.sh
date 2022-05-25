#!/bin/bash

function printcolor {
  COLOR='\033[0;32m'
  NC='\033[0m'
  printf "${COLOR}$1${NC}\n"
}

printcolor "Getting required variables"
export CLUSTER_NAME=$(kubectl get infrastructure cluster -o jsonpath="{.status.infrastructureName}")
export GCP_PROJECT=$(kubectl get infrastructure cluster -o jsonpath="{.status.platformStatus.gcp.projectID}")
export GCP_REGION=$(kubectl get infrastructure cluster -o jsonpath="{.status.platformStatus.gcp.region}")
export INFRASTRUCTURE_KIND=GCPCluster

printcolor "Creating GCP infrastructure cluster"
envsubst <hack/clusters/templates/gcp.yaml | kubectl apply -f -

printcolor "Creating core cluster"
envsubst <hack/clusters/templates/core.yaml | kubectl apply -f -

printcolor "Done"
