package main

import (
	"fmt"
	"strings"

	certmangerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/google/go-containerregistry/pkg/name"
	admissionregistration "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// Workload annotations are used by the workload admission webhook to modify pod
	// resources and correctly schedule them while also pinning them to specific CPUSets.
	// See for more info:
	// https://github.com/openshift/enhancements/blob/master/enhancements/workload-partitioning/wide-availability-workload-partitioning.md
	openshiftWorkloadAnnotation = map[string]string{
		"target.workload.openshift.io/management": `{"effect": "PreferredDuringScheduling"}`,
	}

	// The expected registry for images used by the cluster-capi-operator.
	expectedRegistry = "registry.ci.openshift.org"
)

func processObjects(objs []client.Object, opts cmdlineOptions) ([]client.Object, error) {
	providerConfigMapObjs := make([]client.Object, 0, len(objs))

	serviceSecretNames := findWebhookServiceSecretName(objs)

	var extraObjects []client.Object

	for _, obj := range objs {
		switch getGroup(obj) {
		case "admissionregistration.k8s.io":
			switch getKind(obj) {
			case "MutatingWebhookConfiguration", "ValidatingWebhookConfiguration":
				replaceCertManagerAnnotations(obj)
			}

		case "apiextensions.k8s.io":
			switch getKind(obj) {
			case "CustomResourceDefinition":
				replaceCertManagerAnnotations(obj)

				// Generate a protection policy for an InfraCluster
				// If the user provided a specific InfraCluster resource name, match it exactly.
				// Otherwise, match any CRD in the 'infrastructure.cluster.x-k8s.io' group that ends in 'clusters'.
				crd := &apiextensionsv1.CustomResourceDefinition{}
				mustConvert(obj, crd)
				if (opts.protectClusterResource != "" && crd.Spec.Names.Singular == opts.protectClusterResource) ||
					(opts.protectClusterResource == "" && crd.Spec.Group == "infrastructure.cluster.x-k8s.io" && strings.HasSuffix(crd.Spec.Names.Plural, "clusters")) {
					protectionPolicy := generateInfraClusterProtectionPolicy(crd)
					extraObjects = append(extraObjects, protectionPolicy...)
				}
			}

		case "": // core API group
			switch getKind(obj) {
			case "Service":
				replaceCertMangerServiceSecret(obj, serviceSecretNames)

			case "Namespace", "Secret":
				// Don't emit these resources
				continue
			}

		case "apps":
			switch getKind(obj) {
			case "Deployment":
				deploy, err := customizeDeployment(obj)
				if err != nil {
					return nil, fmt.Errorf("failed to customize deployment %q: %w", obj.GetName(), err)
				}
				obj = deploy
			}

		case "cert-manager.io":
			// Upstream CAPI uses cert-manager.io for cert management by
			// default, and most providers will use it too. Don't emit anything
			// related to cert-manager.
			continue
		}

		providerConfigMapObjs = append(providerConfigMapObjs, obj)
	}

	providerConfigMapObjs = append(providerConfigMapObjs, extraObjects...)

	return providerConfigMapObjs, nil
}

func findWebhookServiceSecretName(objs []client.Object) map[string]string {
	serviceSecretNames := map[string]string{}
	certSecretNames := map[string]string{}

	secretFromCertNN := func(certNN string) (string, bool) {
		if len(certNN) == 0 {
			return "", false
		}
		certName := strings.Split(certNN, "/")[1]
		secretName, ok := certSecretNames[certName]
		if !ok || secretName == "" {
			return "", false
		}
		return secretName, true
	}

	// find service, then cert, then secret
	// return map[certName] = secretName
	for _, obj := range objs {
		switch getKind(obj) {
		case "Certificate":
			cert := &certmangerv1.Certificate{}
			mustConvert(obj, cert)

			certSecretNames[cert.Name] = cert.Spec.SecretName
		}
	}

	for _, obj := range objs {
		switch getKind(obj) {
		case "CustomResourceDefinition":
			crd := &apiextensionsv1.CustomResourceDefinition{}
			mustConvert(obj, crd)

			if certNN, ok := crd.Annotations["cert-manager.io/inject-ca-from"]; ok {
				secretName, ok := secretFromCertNN(certNN)
				if !ok {
					panic("can't find secret from cert: " + certNN)
				}
				if crd.Spec.Conversion != nil {
					serviceSecretNames[crd.Spec.Conversion.Webhook.ClientConfig.Service.Name] = secretName
				}
			}

		case "MutatingWebhookConfiguration":
			mwc := &admissionregistration.MutatingWebhookConfiguration{}
			mustConvert(obj, mwc)

			if certNN, ok := mwc.Annotations["cert-manager.io/inject-ca-from"]; ok {
				secretName, ok := secretFromCertNN(certNN)
				if !ok {
					panic("can't find secret from cert: " + certNN)
				}
				serviceSecretNames[mwc.Webhooks[0].ClientConfig.Service.Name] = secretName
			}

		case "ValidatingWebhookConfiguration":
			vwc := &admissionregistration.ValidatingWebhookConfiguration{}
			mustConvert(obj, vwc)

			if certNN, ok := vwc.Annotations["cert-manager.io/inject-ca-from"]; ok {
				secretName, ok := secretFromCertNN(certNN)
				if !ok {
					panic("can't find secret from cert:CustomResourceDefinition " + certNN)
				}
				serviceSecretNames[vwc.Webhooks[0].ClientConfig.Service.Name] = secretName
			}
		}
	}
	return serviceSecretNames
}

func customizeDeployment(obj client.Object) (client.Object, error) {
	deployment := &appsv1.Deployment{}
	mustConvert(obj, deployment)

	deployment.Spec.Template.Spec.PriorityClassName = "system-cluster-critical"

	deployment.Spec.Template.Annotations = mergeMaps(deployment.Spec.Template.Annotations, openshiftWorkloadAnnotation)

	for i := range deployment.Spec.Template.Spec.Containers {
		container := &deployment.Spec.Template.Spec.Containers[i]
		// Add resource requests
		container.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		}
		// Remove any existing resource limits. See: https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#resources-and-limits
		container.Resources.Limits = corev1.ResourceList{}

		// This helps with debugging and is enforced in OCP, see https://issues.redhat.com/browse/OCPBUGS-33170.
		container.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError

		// We expect all images to use registry.ci.openshift.org. Other images won't be replaced, which would be an error.
		ref, err := name.ParseReference(container.Image)
		if err != nil {
			return nil, fmt.Errorf("failed to parse image reference %q: %w", container.Image, err)
		}
		if ref.Context().RegistryStr() != expectedRegistry {
			return nil, fmt.Errorf("image %q has registry %q, expected %q", container.Image, ref.Context().RegistryStr(), expectedRegistry)
		}
	}

	return deployment, nil
}

func replaceCertManagerAnnotations(obj client.Object) {
	anns := obj.GetAnnotations()
	if anns == nil {
		anns = map[string]string{}
	}
	if _, ok := anns["cert-manager.io/inject-ca-from"]; ok {
		anns["service.beta.openshift.io/inject-cabundle"] = "true"
		delete(anns, "cert-manager.io/inject-ca-from")
		obj.SetAnnotations(anns)
	}
}

func replaceCertMangerServiceSecret(obj client.Object, serviceSecretNames map[string]string) {
	anns := obj.GetAnnotations()
	if anns == nil {
		anns = map[string]string{}
	}
	if name, ok := serviceSecretNames[obj.GetName()]; ok {
		anns["service.beta.openshift.io/serving-cert-secret-name"] = name
		obj.SetAnnotations(anns)
	}
}

// Variadic function to merge maps of like kind.
// Note: keys of next map will override keys in previous map if previous map contains same key.
func mergeMaps[K comparable, V any](maps ...map[K]V) map[K]V {
	result := map[K]V{}
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// generateInfraClusterProtectionPolicy generates a Validating Admission Policy and Binding for protecting
// InfraClusters created by the cluster-capi-operator from deletion and editing.
func generateInfraClusterProtectionPolicy(crd *apiextensionsv1.CustomResourceDefinition) []client.Object {
	var policy client.Object = &admissionregistration.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-cluster-api-protect-" + crd.Spec.Names.Singular,
		},
		Spec: admissionregistration.ValidatingAdmissionPolicySpec{
			FailurePolicy: ptr.To(admissionregistration.Fail),
			ParamKind: &admissionregistration.ParamKind{
				APIVersion: "config.openshift.io/v1",
				Kind:       "Infrastructure",
			},
			MatchConstraints: &admissionregistration.MatchResources{
				ResourceRules: []admissionregistration.NamedRuleWithOperations{
					{
						RuleWithOperations: admissionregistration.RuleWithOperations{
							Operations: []admissionregistration.OperationType{admissionregistration.Delete},
							Rule: admissionregistration.Rule{
								APIGroups:   []string{crd.Spec.Group},
								APIVersions: []string{"*"},
								Resources:   []string{crd.Spec.Names.Plural},
							},
						},
					},
				},
			},
			Validations: []admissionregistration.Validation{
				{
					Expression: "!(oldObject.metadata.name == params.status.infrastructureName)",
					Message:    "InfraCluster resources with metadata.name corresponding to the cluster infrastructureName cannot be deleted.",
				},
			},
		},
	}

	binding := &admissionregistration.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: policy.GetName(),
		},
		Spec: admissionregistration.ValidatingAdmissionPolicyBindingSpec{
			ParamRef: &admissionregistration.ParamRef{
				Name:                    "cluster",
				ParameterNotFoundAction: ptr.To(admissionregistration.DenyAction),
			},
			PolicyName:        policy.GetName(),
			ValidationActions: []admissionregistration.ValidationAction{admissionregistration.Deny},
			MatchResources: &admissionregistration.MatchResources{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"kubernetes.io/metadata.name": capiNamespace,
					},
				},
			},
		},
	}

	// Set type metadata explicitly so it is present in the serialisation
	for _, obj := range []client.Object{policy, binding} {
		setTypeMetadataFromScheme(obj, "v1")
	}

	// ValidatingAdmissionPolicy serialises with a redundant `status` field
	policy = stripStatus(policy)

	return []client.Object{policy, binding}
}

// stripStatus removes the status field from the serialisation of an object.
func stripStatus(obj client.Object) client.Object {
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		panic(err)
	}
	delete(unstructuredObj, "status")
	return &unstructured.Unstructured{Object: unstructuredObj}
}

func setTypeMetadataFromScheme(obj client.Object, version string) {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		panic(err)
	}

	// Get the GVK for the given version
	for _, gvk := range gvks {
		if gvk.Version == version {
			obj.GetObjectKind().SetGroupVersionKind(gvk)
			return
		}
	}
	panic("no " + version + " GVK found")
}
