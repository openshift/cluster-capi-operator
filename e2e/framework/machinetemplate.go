package framework

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// GetAWSMachineTemplateByName gets awsMachineTemplate by its name.
func GetAWSMachineTemplateByName(cl client.Client, name string, namespace string) (*awsv1.AWSMachineTemplate, error) {
	if name == "" {
		return nil, fmt.Errorf("AWSMachineTemplate name cannot be empty")
	}

	awsMachineTemplate := &awsv1.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(awsMachineTemplate), time.Minute, RetryShort).Should(Succeed(), "Failed to get AWSMachineTemplate %s/%s.", awsMachineTemplate.Namespace, awsMachineTemplate.Name)

	return awsMachineTemplate, nil
}

// DeleteAWSMachineTemplates deletes the specified awsMachineTemplates.
func DeleteAWSMachineTemplates(ctx context.Context, cl client.Client, templates ...*awsv1.AWSMachineTemplate) {
	for _, template := range templates {
		if template == nil {
			continue
		}
		By(fmt.Sprintf("Deleting awsMachineTemplate %q", template.GetName()))
		Eventually(func() error {
			return cl.Delete(ctx, template)
		}, time.Minute, RetryShort).Should(SatisfyAny(
			Succeed(),
			WithTransform(apierrors.IsNotFound, BeTrue()),
		), "Delete awsMachineTemplate %s/%s should succeed, or awsMachineTemplate should not be found.",
			template.Namespace, template.Name)
	}
}

// GetAWSMachineTemplateByPrefix gets awsMachineTemplate by its prefix.
func GetAWSMachineTemplateByPrefix(cl client.Client, prefix string, namespace string) (*awsv1.AWSMachineTemplate, error) {
	if prefix == "" {
		return nil, nil
	}
	templateList := &awsv1.AWSMachineTemplateList{}
	Eventually(komega.List(templateList, client.InNamespace(namespace))).Should(Succeed(), "failed to list AWSMachineTemplates in namespace %s.", namespace)

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
func DeleteAWSMachineTemplateByPrefix(ctx context.Context, cl client.Client, prefix string, namespace string) error {
	if prefix == "" {
		return nil
	}
	templateList := &awsv1.AWSMachineTemplateList{}
	Eventually(komega.List(templateList, client.InNamespace(namespace))).Should(Succeed(), "failed to list AWSMachineTemplates in namespace %s.", namespace)

	for i := range templateList.Items {
		if strings.HasPrefix(templateList.Items[i].Name, prefix) {
			Eventually(func() error {
				return cl.Delete(ctx, &templateList.Items[i])
			}, time.Minute, RetryShort).Should(SatisfyAny(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			), "Delete should succeed, or AWSMachineTemplate should not be found")
		}
	}

	return nil
}
