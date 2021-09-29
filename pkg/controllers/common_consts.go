package controllers

const (
	DefaultManagedNamespace = "openshift-cluster-api"

	infrastructureResourceName = "cluster"
	clusterOperatorName        = "cluster-api"
	operatorVersionKey         = "operator"
	externalFeatureGateName    = "cluster"

	// ClusterAPIEnabled is the name of the cluster API feature gate.
	// This is used to enable the cluster API.
	ClusterAPIEnabled = "ClusterAPIEnabled"

	specHashAnnotation = "openshift.io/spec-hash"
)
