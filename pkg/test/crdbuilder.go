/*
Copyright 2024 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package test

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"unicode"

	"github.com/gobuffalo/flect"
	"github.com/onsi/gomega"
	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/yaml"
)

var (
	// operatorGroupVersion is group version used for operatora objects.
	operatorGroupVersion = schema.GroupVersion{Group: "operator.cluster.x-k8s.io", Version: "v1alpha2"}

	// fakeCoreProviderKind is the Kind for the CoreProvider.
	fakeCoreProviderKind = "CoreProvider"
	// fakeCoreProviderCRD is a fake CoreProvider CRD.
	fakeCoreProviderCRD = GenerateCRD(operatorGroupVersion.WithKind(fakeCoreProviderKind))

	// fakeInfrastructureProviderKind is the Kind for the CoreProvider.
	fakeInfrastructureProviderKind = "InfrastructureProvider"

	// fakeInfrastructureProviderCRD is a fake CoreProvider CRD.
	fakeInfrastructureProviderCRD = GenerateCRD(operatorGroupVersion.WithKind(fakeInfrastructureProviderKind))

	// clusterGroupVersion is group version used for cluster objects.
	clusterGroupVersion = schema.GroupVersion{Group: "cluster.x-k8s.io", Version: "v1beta2"}

	// fakeClusterKind is the Kind for the Cluster.
	fakeClusterKind = "Cluster"

	// fakeClusterCRD is a fake Cluster CRD.
	fakeClusterCRD = GenerateCRD(clusterGroupVersion.WithKind(fakeClusterKind))

	// fakeMachineKind is the Kind for the Machine.
	fakeMachineKind = "Machine"

	// fakeMachineCRD is a fake Machine CRD.
	fakeMachineCRD = GenerateCRD(clusterGroupVersion.WithKind(fakeMachineKind))

	// fakeMachineSetKind is the kind for the MachineSet.
	fakeMachineSetKind = "MachineSet"

	// fakeMachineSetCRD is a fake MachineSet CRD.
	fakeMachineSetCRD = GenerateCRD(clusterGroupVersion.WithKind(fakeMachineSetKind))

	// v1beta1InfrastructureGroupVersion is a v1beta1 group version used for infrastructure objects.
	v1beta1InfrastructureGroupVersion = schema.GroupVersion{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1"}

	// fakeOpenStackClusterKind is the Kind for the OpenStackCluster.
	fakeOpenStackClusterKind = "OpenStackCluster"

	// fakeOpenStackClusterCRD is a fake OpenStackCluster CRD.
	fakeOpenStackClusterCRD = GenerateCRD(v1beta1InfrastructureGroupVersion.WithKind(fakeOpenStackClusterKind))

	// fakeOpenStackMachineTemplateKind is the kind for the OpenStackMachineTemplate.
	fakeOpenStackMachineTemplateKind = "OpenStackMachineTemplate"

	// fakeOpenStackMachineTemplateCRD is a fake OpenStackMachineTemplate CRD.
	fakeOpenStackMachineTemplateCRD = GenerateCRD(v1beta1InfrastructureGroupVersion.WithKind(fakeOpenStackMachineTemplateKind))

	// mapiv1GroupVersion is group version used for MAPI v1 objects.
	mapiv1GroupVersion = schema.GroupVersion{Group: "machine.openshift.io", Version: "v1"}

	// fakeControlPlaneMachineSetKind is the Kind for the ControlPlaneMachineSet.
	fakeControlPlaneMachineSetKind = "ControlPlaneMachineSet"

	// fakeControlPlaneMachineSetCRD is a fake ControlPlaneMachineSet CRD.
	fakeControlPlaneMachineSetCRD = GenerateCRD(mapiv1GroupVersion.WithKind(fakeControlPlaneMachineSetKind))

	// v1beta2InfrastructureGroupVersion is a v1beta2 group version used for infrastructure objects.
	v1beta2InfrastructureGroupVersion = schema.GroupVersion{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta2"}

	// fakeAWSClusterKind is the Kind for the AWSCluster.
	fakeAWSClusterKind = "AWSCluster"

	// fakeAWSClusterCRD is a fake AWSCluster CRD.
	fakeAWSClusterCRD = generateProviderCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeAWSClusterKind))

	// fakeAWSMachineTemplateKind is the kind for the AWSMachineTemplate.
	fakeAWSMachineTemplateKind = "AWSMachineTemplate"

	// fakeAWSMachineTemplateCRD is a fake AWSMachineTemplate CRD.
	fakeAWSMachineTemplateCRD = generateProviderCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeAWSMachineTemplateKind))

	// fakeAWSMachineKind is the kind for the AWSMachine.
	fakeAWSMachineKind = "AWSMachine"

	// fakeAWSMachineCRD is a fake AWSMachine CRD.
	fakeAWSMachineCRD = generateProviderCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeAWSMachineKind))

	// fakeAzureClusterKind is the Kind for the AWSCluster.
	fakeAzureClusterKind = "AzureCluster"

	// fakeAWSClusterCRD is a fake AzureCluster CRD.
	fakeAzureClusterCRD = generateProviderCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeAzureClusterKind))

	// fakeGCPClusterKind is the Kind for the GCPCluster.
	fakeGCPClusterKind = "GCPCluster"

	// fakeGCPClusterCRD is a fake GCPCluster CRD.
	fakeGCPClusterCRD = generateProviderCRD(v1beta2InfrastructureGroupVersion.WithKind(fakeGCPClusterKind))
)

// GenerateCRD generates a fake CustomResourceDefinition for a given
// GroupVersionKind for use in tests. It may optionally be given a set of
// additional versions to include in the CRD. The additional versions will be
// added before the primary version. Only the primary version will be marked as
// a storage version.
func GenerateCRD(gvk schema.GroupVersionKind, additionalVersions ...string) *apiextensionsv1.CustomResourceDefinition {
	generateVersion := func(version string) apiextensionsv1.CustomResourceDefinitionVersion {
		return apiextensionsv1.CustomResourceDefinitionVersion{
			Name:    version,
			Served:  true,
			Storage: version == gvk.Version,
			Subresources: &apiextensionsv1.CustomResourceSubresources{
				Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
			},
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:                   "object",
							XPreserveUnknownFields: ptr.To(true),
						},
						"status": {
							Type:                   "object",
							XPreserveUnknownFields: ptr.To(true),
						},
					},
				},
			},
		}
	}

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
			Versions: append(util.SliceMap(additionalVersions, generateVersion), generateVersion(gvk.Version)),
		},
	}
}

// GenerateTestCRD generates a simple CRD with a randomly generated Kind.
// Version is always v1.
// Group is always example.com.
func GenerateTestCRD() *apiextensionsv1.CustomResourceDefinition {
	const validChars = "abcdefghijklmnopqrstuvwxyz"

	randBytes := make([]byte, 10)

	for i := range randBytes {
		randInt, err := rand.Int(rand.Reader, big.NewInt(int64(len(validChars))))
		gomega.Expect(err).To(gomega.Succeed())

		randBytes[i] = validChars[randInt.Int64()]
	}

	gvk := schema.GroupVersionKind{
		Group:   "example.com",
		Version: "v1",
		Kind:    string(unicode.ToUpper(rune(randBytes[0]))) + string(randBytes[1:]),
	}

	return GenerateCRD(gvk)
}

// GenerateTestCompatibilityRequirement generates a simple CompatibilityRequirement using the given CRD as the CompatibilityCRD.
// The generated requirement uses GenerateName, so it will not have a valid Name until it is created.
func GenerateTestCompatibilityRequirement(testCRD *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1alpha1.CompatibilityRequirement {
	yaml, err := yaml.Marshal(testCRD)
	gomega.Expect(err).To(gomega.Succeed())

	return &apiextensionsv1alpha1.CompatibilityRequirement{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-requirement-",
		},
		Spec: apiextensionsv1alpha1.CompatibilityRequirementSpec{
			CompatibilitySchema: apiextensionsv1alpha1.CompatibilitySchema{
				CustomResourceDefinition: apiextensionsv1alpha1.CRDData{
					Type: apiextensionsv1alpha1.CRDDataTypeYAML,
					Data: string(yaml),
				},
				RequiredVersions: apiextensionsv1alpha1.APIVersions{
					DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeAllServed,
				},
			},
			CustomResourceDefinitionSchemaValidation: apiextensionsv1alpha1.CustomResourceDefinitionSchemaValidation{
				Action: apiextensionsv1alpha1.CRDAdmitActionDeny,
			},
		},
		Status: apiextensionsv1alpha1.CompatibilityRequirementStatus{
			CRDName: testCRD.GetName(),
		},
	}
}

func generateProviderCRD(gvk schema.GroupVersionKind) *apiextensionsv1.CustomResourceDefinition {
	crd := GenerateCRD(gvk)

	if crd.ObjectMeta.Labels == nil {
		crd.ObjectMeta.Labels = make(map[string]string)
	}

	crd.ObjectMeta.Labels[fmt.Sprintf("%s/%s", clusterv1.GroupVersion.Group, clusterv1.GroupVersion.Version)] = gvk.GroupVersion().Version

	return crd
}
