package framework

import (
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetAWSMachineTemplateByName gets awsMachineTemplate by its name from the default cluster API namespace.
func GetAWSMachineTemplateByName(cl client.Client, name string) (*awsv1.AWSMachineTemplate, error) {
	var awsMachineTemplate = &awsv1.AWSMachineTemplate{}

	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}
	if err := cl.Get(ctx, key, awsMachineTemplate); err != nil {
		return nil, err
	}
	return awsMachineTemplate, nil
}

// GetAWSMachineTemplateByPrefix gets awsMachineTemplate by its prefix from the default cluster API namespace.
func GetAWSMachineTemplateByPrefix(cl client.Client, prefix string) (*awsv1.AWSMachineTemplate, error) {
    templateList := &awsv1.AWSMachineTemplateList{}
    if err := cl.List(ctx, templateList, client.InNamespace(CAPINamespace)); err != nil {
        return nil, fmt.Errorf("failed to list AWSMachineTemplates: %w", err)
    }

    var matches []*awsv1.AWSMachineTemplate
    for i, t := range templateList.Items {
        if strings.HasPrefix(t.Name, prefix) {
            matches = append(matches, &templateList.Items[i])
        }
    }

    switch len(matches) {
    case 0:
        return nil, fmt.Errorf("no AWSMachineTemplate found with prefix %q", prefix)
    case 1:
        return matches[0], nil
    default:
        return nil, fmt.Errorf("multiple AWSMachineTemplates found with prefix %q (%d matches)", prefix, len(matches))
    }
}

// DeleteAWSMachineTemplateByPrefix deletes all AWSMachineTemplates with matching name prefix
func DeleteAWSMachineTemplateByPrefix(cl client.Client, prefix string) error {
    templateList := &awsv1.AWSMachineTemplateList{}
    if err := cl.List(ctx, templateList, client.InNamespace(CAPINamespace)); err != nil {
        return fmt.Errorf("failed to list AWSMachineTemplates: %w", err)
    }

    var deleteErrors []error
    var deletedCount int

    for i := range templateList.Items {
        if strings.HasPrefix(templateList.Items[i].Name, prefix) {
            if err := cl.Delete(ctx, &templateList.Items[i]); err != nil {
                if !apierrors.IsNotFound(err) {
                    deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete %s: %w", templateList.Items[i].Name, err))
                }
                continue
            }
            deletedCount++
        }
    }

    if len(deleteErrors) > 0 {
        return fmt.Errorf("deleted %d templates, but encountered %d errors: %v", 
            deletedCount, len(deleteErrors), errors.Join(deleteErrors...))
    }

    if deletedCount == 0 {
        return fmt.Errorf("no templates found with prefix %q", prefix)
    }

    return nil
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
