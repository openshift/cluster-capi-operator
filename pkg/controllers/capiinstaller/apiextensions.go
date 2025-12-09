/*
Copyright 2025 Red Hat, Inc.

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
package capiinstaller

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcehelper"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// applyCustomResourceDefinitionV1Improved applies the required CustomResourceDefinition to the cluster.
//
//nolint:forcetypeassert
func applyCustomResourceDefinitionV1Improved(ctx context.Context, client apiextclientv1.CustomResourceDefinitionsGetter, recorder events.Recorder, required *apiextensionsv1.CustomResourceDefinition) (*apiextensionsv1.CustomResourceDefinition, bool, error) {
	existing, err := client.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		requiredCopy := required.DeepCopy()
		actual, err := client.CustomResourceDefinitions().Create(
			ctx, resourcemerge.WithCleanLabelsAndAnnotations(requiredCopy).(*apiextensionsv1.CustomResourceDefinition), metav1.CreateOptions{})
		resourcehelper.ReportCreateEvent(recorder, required, err)

		return actual, true, fmt.Errorf("error creating CustomResourceDefinition %q: %w", required.Name, err)
	}

	if err != nil {
		return nil, false, fmt.Errorf("error getting CustomResourceDefinition %q: %w", required.Name, err)
	}

	modified := false
	existingCopy := existing.DeepCopy()

	ensureCustomResourceDefinitionV1CaBundle(required, *existing)

	resourcemerge.EnsureCustomResourceDefinitionV1(&modified, existingCopy, *required)

	if !modified {
		return existing, false, nil
	}

	if klog.V(2).Enabled() {
		klog.Infof("CustomResourceDefinition %q changes: %s", existing.Name, resourceapply.JSONPatchNoError(existing, existingCopy))
	}

	actual, err := client.CustomResourceDefinitions().Update(ctx, existingCopy, metav1.UpdateOptions{})
	resourcehelper.ReportUpdateEvent(recorder, required, err)

	return actual, true, fmt.Errorf("error updating CustomResourceDefinition %q: %w", required.Name, err)
}

// injectCABundleAnnotation is the annotation used to indicate into which resources
// the service-ca controller should inject the CA bundle.
const injectCABundleAnnotation = "service.beta.openshift.io/inject-cabundle"

// ensureCustomResourceDefinitionV1CaBundle ensures that the field
// spec.Conversion.Webhook.ClientConfig.CABundle of a CRD is not managed by the CVO when
// the service-ca controller is responsible for the field.
// Note: this is the same way as CVO does it https://github.com/openshift/cluster-version-operator/blob/0e6c916f99e05983190202575bb530200560acb9/lib/resourcemerge/apiext.go#L34
func ensureCustomResourceDefinitionV1CaBundle(required *apiextensionsv1.CustomResourceDefinition, existing apiextensionsv1.CustomResourceDefinition) {
	if val, ok := existing.ObjectMeta.Annotations[injectCABundleAnnotation]; !ok || val != "true" {
		return
	}

	req := required.Spec.Conversion
	if req == nil ||
		req.Webhook == nil ||
		req.Webhook.ClientConfig == nil {
		return
	}

	if req.Strategy != apiextensionsv1.WebhookConverter {
		// The service CA bundle is only injected by the service-ca controller into
		// the CRD if the CRD is configured to use a webhook for conversion
		return
	}

	exc := existing.Spec.Conversion
	if exc != nil &&
		exc.Webhook != nil &&
		exc.Webhook.ClientConfig != nil {
		req.Webhook.ClientConfig.CABundle = exc.Webhook.ClientConfig.CABundle
	}
}
