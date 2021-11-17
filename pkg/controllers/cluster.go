package controllers

import (
	"strings"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1alpha4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha4"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1alpha4"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha4"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
)

var (
	infraFn = map[configv1.PlatformType]func(cluster *clusterv1.Cluster, infra *configv1.Infrastructure) []client.Object{
		configv1.AWSPlatformType:       awsClusterObjects,
		configv1.GCPPlatformType:       gcpClusterObjects,
		configv1.AzurePlatformType:     azureClusterObjects,
		configv1.BareMetalPlatformType: metal3ClusterObjects,
		configv1.OpenStackPlatformType: openStackClusterObjects,
	}
)

func (r *ClusterOperatorReconciler) clusterObjects() []client.Object {
	if !r.currentProviderSupportedByCAPI() {
		return []client.Object{}
	}
	c := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster",
			Namespace: DefaultManagedNamespace,
		},
	}

	return infraFn[r.infra.Spec.PlatformSpec.Type](c, r.infra)
}

func (r *ClusterOperatorReconciler) currentProviderSupportedByCAPI() bool {
	_, supported := infraFn[r.infra.Spec.PlatformSpec.Type]
	return supported
}

// https://github.com/kubernetes-sigs/cluster-api/blob/main/cmd/clusterctl/client/config/providers_client.go#L36-L47
func (r *ClusterOperatorReconciler) currentProviderName() string {
	switch r.infra.Status.PlatformStatus.Type {
	case configv1.LibvirtPlatformType, configv1.NonePlatformType, configv1.OvirtPlatformType, configv1.EquinixMetalPlatformType:
		return "" // no equivilent in capi
	case configv1.BareMetalPlatformType:
		return "metal3"
	default:
		return strings.ToLower(string(r.infra.Status.PlatformStatus.Type))
	}
}

func awsClusterObjects(cluster *clusterv1.Cluster, infra *configv1.Infrastructure) []client.Object {
	awsCluster := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cluster.Namespace,
			Name:        "aws",
			Annotations: map[string]string{"cluster.x-k8s.io/managed-by": ""},
		},
	}
	if infra.Status.PlatformStatus != nil && infra.Status.PlatformStatus.AWS != nil {
		awsCluster.Spec.Region = infra.Status.PlatformStatus.AWS.Region
	}

	cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
		Kind:       "AWSCluster",
		Namespace:  awsCluster.Namespace,
		Name:       awsCluster.Name,
	}

	return []client.Object{cluster, awsCluster}
}

func gcpClusterObjects(cluster *clusterv1.Cluster, infra *configv1.Infrastructure) []client.Object {
	gcpCluster := &gcpv1.GCPCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cluster.Namespace,
			Name:        "gcp",
			Annotations: map[string]string{"cluster.x-k8s.io/managed-by": ""},
		},
	}
	if infra.Status.PlatformStatus != nil && infra.Status.PlatformStatus.GCP != nil {
		gcpCluster.Spec.Region = infra.Status.PlatformStatus.GCP.Region
	}

	cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
		Kind:       "GCPCluster",
		Namespace:  gcpCluster.Namespace,
		Name:       gcpCluster.Name,
	}

	return []client.Object{cluster, gcpCluster}
}

func azureClusterObjects(cluster *clusterv1.Cluster, infra *configv1.Infrastructure) []client.Object {
	azureCluster := &azurev1.AzureCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cluster.Namespace,
			Name:        "azure",
			Annotations: map[string]string{"cluster.x-k8s.io/managed-by": ""},
		},
	}
	if infra.Status.PlatformStatus != nil && infra.Status.PlatformStatus.Azure != nil {
		azureCluster.Spec.ResourceGroup = infra.Status.PlatformStatus.Azure.ResourceGroupName
		azureCluster.Spec.AzureEnvironment = string(infra.Status.PlatformStatus.Azure.CloudName)
	}

	cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
		Kind:       "AzureCluster",
		Namespace:  azureCluster.Namespace,
		Name:       azureCluster.Name,
	}

	return []client.Object{cluster, azureCluster}
}

func metal3ClusterObjects(cluster *clusterv1.Cluster, _ *configv1.Infrastructure) []client.Object {
	metal3Cluster := &metal3v1.Metal3Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cluster.Namespace,
			Name:        "metal3",
			Annotations: map[string]string{"cluster.x-k8s.io/managed-by": ""},
		},
	}

	cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
		Kind:       "Metal3Cluster",
		Namespace:  metal3Cluster.Namespace,
		Name:       metal3Cluster.Name,
	}

	return []client.Object{cluster, metal3Cluster}
}

func openStackClusterObjects(cluster *clusterv1.Cluster, infra *configv1.Infrastructure) []client.Object {
	osCluster := &openstackv1.OpenStackCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cluster.Namespace,
			Name:        "openstack",
			Annotations: map[string]string{"cluster.x-k8s.io/managed-by": ""},
		},
	}
	if infra.Status.PlatformStatus != nil && infra.Status.PlatformStatus.OpenStack != nil {
		osCluster.Spec.APIServerFloatingIP = infra.Status.PlatformStatus.OpenStack.APIServerInternalIP
		osCluster.Spec.CloudName = infra.Status.PlatformStatus.OpenStack.CloudName
	}

	cluster.Spec.InfrastructureRef = &corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
		Kind:       "OpenStackCluster",
		Namespace:  osCluster.Namespace,
		Name:       osCluster.Name,
	}

	return []client.Object{cluster, osCluster}
}
