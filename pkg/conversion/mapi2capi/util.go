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

package mapi2capi

import (
	"fmt"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func convertMAPIMachineSetSelectorToCAPI(mapiSelector metav1.LabelSelector) metav1.LabelSelector {
	capiSelector := mapiSelector.DeepCopy()
	capiSelector.MatchLabels = convertMAPILabelsToCAPI(mapiSelector.MatchLabels)

	return *capiSelector
}

func convertMAPILabelsToCAPI(mapiLabels map[string]string) map[string]string {
	// These labels should not be converted to CAPI labels as they are handled explicitly in the conversion logic.
	mapiMetadataLabelsToSkip := sets.New(consts.MAPIMachineMetadataLabelInstanceType, consts.MAPIMachineMetadataLabelRegion, consts.MAPIMachineMetadataLabelZone)

	capiLabels := make(map[string]string)

	toTransformLabels := map[string]func(string) (string, string){
		"machine.openshift.io/cluster-api-machine-type": func(mapiLabelValue string) (string, string) {
			return fmt.Sprintf("%s/%s", clusterv1beta1.NodeRoleLabelPrefix, mapiLabelValue), ""
		},
		"machine.openshift.io/cluster-api-machine-role": func(mapiLabelValue string) (string, string) {
			return fmt.Sprintf("%s/%s", clusterv1beta1.NodeRoleLabelPrefix, mapiLabelValue), ""
		},
	}

	for mapiLabelKey, mapiLabelVal := range mapiLabels {
		if transformFunc, ok := toTransformLabels[mapiLabelKey]; ok {
			capiLabelKey, capiLabelVal := transformFunc(mapiLabelVal)
			capiLabels[capiLabelKey] = capiLabelVal

			continue
		}

		// Ignore MAPI-specific labels that are explicitly handled.
		if mapiMetadataLabelsToSkip.Has(mapiLabelKey) {
			continue
		}

		// Default case - copy over the label as-is to CAPI.
		capiLabels[mapiLabelKey] = mapiLabelVal
	}

	return capiLabels
}

func convertMAPIAnnotationsToCAPI(mapiAnnotations map[string]string) map[string]string {
	if len(mapiAnnotations) == 0 {
		return nil
	}

	// These annotations should not be converted to Cluster API annotations as they are handled explicitly in the conversion logic.
	mapiMetadataAnnotationsToSkip := sets.New(consts.MAPIMachineMetadataAnnotationInstanceState)

	capiAnnotations := make(map[string]string)

	for k, v := range mapiAnnotations {
		if k == util.MapiDeleteMachineAnnotation {
			capiAnnotations[clusterv1beta1.DeleteMachineAnnotation] = v
			continue
		}

		// Ignore MAPI-specific annotations that are explicitly handled.
		if mapiMetadataAnnotationsToSkip.Has(k) {
			continue
		}

		capiAnnotations[k] = v
	}

	if len(capiAnnotations) == 0 {
		return nil
	}

	return capiAnnotations
}

func setMAPINodeAnnotationsToCAPINodeAnnotations(mapiNodeAnnotations map[string]string, capiMachine *clusterv1beta1.Machine) {
	if len(mapiNodeAnnotations) == 0 {
		return
	}

	if capiMachine.Annotations == nil {
		capiMachine.Annotations = map[string]string{}
	}

	for k, v := range mapiNodeAnnotations {
		capiMachine.Annotations[k] = v
	}
}

func setMAPINodeLabelsToCAPINodeLabels(mapiNodeLabels map[string]string, capiMachine *clusterv1beta1.Machine) {
	if len(mapiNodeLabels) == 0 {
		return
	}

	if capiMachine.Labels == nil {
		capiMachine.Labels = map[string]string{}
	}

	for k, v := range mapiNodeLabels {
		capiMachine.Labels[k] = v
	}
}

// setCAPILifecycleHookAnnotations sets the annotations that should be added to a CAPI Machine to represent the lifecycle hooks.
func setCAPILifecycleHookAnnotations(hooks mapiv1beta1.LifecycleHooks, capiMachine *clusterv1beta1.Machine) {
	lifecycleAnnotations := make(map[string]string)

	for _, hook := range hooks.PreDrain {
		lifecycleAnnotations[fmt.Sprintf("%s/%s", clusterv1beta1.PreDrainDeleteHookAnnotationPrefix, hook.Name)] = hook.Owner
	}

	for _, hook := range hooks.PreTerminate {
		lifecycleAnnotations[fmt.Sprintf("%s/%s", clusterv1beta1.PreTerminateDeleteHookAnnotationPrefix, hook.Name)] = hook.Owner
	}

	if len(lifecycleAnnotations) > 0 && capiMachine.Annotations == nil {
		capiMachine.Annotations = make(map[string]string)
	}

	for key, value := range lifecycleAnnotations {
		capiMachine.Annotations[key] = value
	}
}

// handleUnsupportedMachineFields checks for fields that are not supported by CAPI and returns a list of errors.
func handleUnsupportedMachineFields(spec mapiv1beta1.MachineSpec) field.ErrorList {
	var errs field.ErrorList

	fldPath := field.NewPath("spec")

	errs = append(errs, handleUnsupportedMAPIObjectMetaFields(fldPath.Child("metadata"), spec.ObjectMeta)...)

	// TODO(OCPCLOUD-2861/2899): Taints are not supported by CAPI. add support for them via CAPI BootstrapConfig + minimal bootstrap controller.
	if len(spec.Taints) > 0 {
		errs = append(errs, field.Invalid(fldPath.Child("taints"), spec.Taints, "taints are not currently supported"))
	}

	return errs
}

// handleUnsupportedMAPIObjectMetaFields checks for unsupported MAPI metadta fields and returns a list of errors
// if any of them are currently set.
// This is used to prevent usage of these fields in both the Machine and MachineSet specs.
func handleUnsupportedMAPIObjectMetaFields(fldPath *field.Path, objectMeta mapiv1beta1.ObjectMeta) field.ErrorList {
	var errs field.ErrorList

	// ObjectMeta related fields should never get converted (aside from labels and annotations).
	// They are meaningless in MAPI and don't contribute to the logic of the product.
	if objectMeta.Name != "" {
		errs = append(errs, field.Invalid(fldPath.Child("name"), objectMeta.Name, "name is not supported"))
	}

	if objectMeta.GenerateName != "" {
		errs = append(errs, field.Invalid(fldPath.Child("generateName"), objectMeta.GenerateName, "generateName is not supported"))
	}

	if objectMeta.Namespace != "" {
		errs = append(errs, field.Invalid(fldPath.Child("namespace"), objectMeta.Namespace, "namespace is not supported"))
	}

	return errs
}
