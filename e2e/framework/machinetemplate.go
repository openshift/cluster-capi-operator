package framework

import (
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetAWSMachineTemplate gets awsMachineTemplate by its name from the default cluster API namespace.
func GetAWSMachineTemplate(cl client.Client, name string) (*awsv1.AWSMachineTemplate, error) {
	var awsMachineTemplate = &awsv1.AWSMachineTemplate{}

	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}
	if err := cl.Get(ctx, key, awsMachineTemplate); err != nil {
		return nil, err
	}
	return awsMachineTemplate, nil
}

// GetAzureMachineTemplate gets azureMachineTemplate by its name from the default cluster API namespace.
func GetAzureMachineTemplate(cl client.Client, name string) (*azurev1.AzureMachineTemplate, error) {
	var azureMachineTemplate = &azurev1.AzureMachineTemplate{}

	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}
	if err := cl.Get(ctx, key, azureMachineTemplate); err != nil {
		return nil, err
	}
	return azureMachineTemplate, nil
}

// GetGCPMachineTemplate gets gcpMachineTemplate by its name from the default cluster API namespace.
func GetGCPMachineTemplate(cl client.Client, name string) (*gcpv1.GCPMachineTemplate, error) {
	var gcpMachineTemplate = &gcpv1.GCPMachineTemplate{}

	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}
	if err := cl.Get(ctx, key, gcpMachineTemplate); err != nil {
		return nil, err
	}
	return gcpMachineTemplate, nil
}
