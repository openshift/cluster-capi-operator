/*
Copyright 2026 Red Hat, Inc.

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

package revisiongenerator

import (
	"fmt"
	"strings"

	"github.com/drone/envsubst/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

// ManagedLabelKey is a label key used to identify objects managed by the CAPI operator.
const ManagedLabelKey = operatorstatus.CAPIOperatorIdentifierDomain + "/managed-by"

func envSubstSubstitutions(key string) string {
	switch key {
	// Used only in the AWS provider. Eventually, we intend to move this into
	// provider metadata.
	case "EXP_BOOTSTRAP_FORMAT_IGNITION":
		return "true"
	default:
		return ""
	}
}

// IMPORTANT NOTE: changes to transformYaml or transformObject which are not
// dependent on a change in the API revision are breaking changes: it will
// update the revision's content ID without the revision having been updated.
// The controller will recover when a new revision is created, but it will no
// longer be able to reconcile the old revision. This should be done with care.

// transformYaml applies transformations to an object's YAML before it is unmarshalled.
func transformYaml(providerProfile *providerimages.ProviderImageManifests, yaml string, substitutions map[string]string) (string, error) {
	// Expand envsubst variables, checking user-provided substitutions first.
	yaml, err := envsubst.Eval(yaml, func(key string) string {
		if v, ok := substitutions[key]; ok {
			return v
		}

		return envSubstSubstitutions(key)
	})
	if err != nil {
		return "", fmt.Errorf("failed to substitute variables: %w", err)
	}

	// Replace self-image-ref with actual image ref.
	if providerProfile.SelfImageRef != "" {
		yaml = strings.ReplaceAll(yaml, providerProfile.SelfImageRef, providerProfile.ImageRef)
	}

	return yaml, nil
}

// transformObject applies transformations to an object after it is
// unmarshalled.
func transformObject(obj unstructured.Unstructured, componentName string) unstructured.Unstructured {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[ManagedLabelKey] = componentName
	obj.SetLabels(labels)

	return obj
}
