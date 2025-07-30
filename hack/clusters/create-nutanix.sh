#!/bin/bash

function printcolor {
  COLOR='\033[0;32m'
  NC='\033[0m'
  printf "${COLOR}$1${NC}\n"
}

printcolor "Getting required Nutanix variables"
export CLUSTER_NAME=$(kubectl get infrastructure cluster -o jsonpath="{.status.infrastructureName}")
export INFRASTRUCTURE_KIND=NutanixCluster
export CONTROL_PLANE_ENDPOINT_IP=$(kubectl get infrastructure cluster -o jsonpath="{.status.platformStatus.nutanix.apiServerInternalIP}")
export PRISM_CENTRAL_ENDPOINT_IP=$(kubectl get infrastructure cluster -o jsonpath="{.spec.platformSpec.nutanix.prismCentral.address}")
export PRISM_CENTRAL_ENDPOINT_PORT=$(kubectl get infrastructure cluster -o jsonpath="{.spec.platformSpec.nutanix.prismCentral.port}")

printcolor "Creating Nutanix secret for NutanixCluster"
envsubst <hack/clusters/templates/secret-nutanix.yaml | kubectl apply -f -

printcolor "Creating Nutanix infrastructure cluster"
envsubst <hack/clusters/templates/nutanix.yaml | kubectl apply -f -

printcolor "Creating core cluster"
envsubst <hack/clusters/templates/core-nutanix.yaml | kubectl apply -f -

printcolor "Done"
