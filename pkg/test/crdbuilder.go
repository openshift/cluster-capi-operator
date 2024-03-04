package test

import (
	"fmt"
	"strings"

	"github.com/gobuffalo/flect"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// operatorGroupVersion is group version used for operatora objects.
	operatorGroupVersion = schema.GroupVersion{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2"}

	// fakeCoreProviderKind is the Kind for the CoreProvider.
	fakeCoreProviderKind = "CoreProvider"
	// fakeCoreProviderCRD is a fake CoreProvider CRD.
	fakeCoreProviderCRD = generateCRD(operatorGroupVersion.WithKind(fakeCoreProviderKind))

	// fakeInfrastructureProviderKind is the Kind for the CoreProvider.
	fakeInfrastructureProviderKind = "InfrastructureProvider"
	// fakeInfrastructureProviderCRD is a fake CoreProvider CRD.
	fakeInfrastructureProviderCRD = generateCRD(operatorGroupVersion.WithKind(fakeInfrastructureProviderKind))

	// clusterGroupVersion is group version used for cluster objects.
	clusterGroupVersion = schema.GroupVersion{Group: "cluster.x-k8s.io", Version: "v1beta1"}

	// fakeClusterKind is the Kind for the Cluster.
	fakeClusterKind = "Cluster"
	// fakeClusterCRD is a fake Cluster CRD.
	fakeClusterCRD = generateCRD(clusterGroupVersion.WithKind(fakeClusterKind))

	// v1beta2InfrastructureGroupVersion is a v1beta2 group version used for infrastructure objects.
	v1beta2InfrastructureGroupVersion = schema.GroupVersion{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta2"}

	// fakeAWSClusterKind is the Kind for the AWSCluster.
	fakeAWSClusterKind = "AWSCluster"
	// fakeAWSClusterCRD is a fake AWSCluster CRD.
	fakeAWSClusterCRD = generateCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeAWSClusterKind))

	// fakeAzureClusterKind is the Kind for the AWSCluster.
	fakeAzureClusterKind = "AzureCluster"
	// fakeAWSClusterCRD is a fake AzureCluster CRD.
	fakeAzureClusterCRD = generateCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeAzureClusterKind))

	// fakeGCPClusterKind is the Kind for the GCPCluster.
	fakeGCPClusterKind = "GCPCluster"
	// fakeGCPClusterCRD is a fake GCPCluster CRD.
	fakeGCPClusterCRD = generateCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeGCPClusterKind))
)

func generateCRD(gvk schema.GroupVersionKind) *apiextensionsv1.CustomResourceDefinition {
	shouldPreserveUnknownFields := true
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s.%s", flect.Pluralize(strings.ToLower(gvk.Kind)), gvk.Group),
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: gvk.Group,
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   gvk.Kind,
				Plural: flect.Pluralize(strings.ToLower(gvk.Kind)),
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    gvk.Version,
					Served:  true,
					Storage: true,
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type:                   "object",
									XPreserveUnknownFields: &shouldPreserveUnknownFields,
								},
								"status": {
									Type:                   "object",
									XPreserveUnknownFields: &shouldPreserveUnknownFields,
								},
							},
						},
					},
				},
			},
		},
	}
}
