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
	"time"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

const (
	capiNamespace = "openshift-cluster-api"
)

// fromMAPIMachineToCAPIMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMAPIMachineToCAPIMachine(mapiMachine *mapiv1beta1.Machine, apiVersion, kind string) (*clusterv1.Machine, field.ErrorList) {
	var errs field.ErrorList

	capiMachineStatus, capiMachineStatusErrs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
	if len(capiMachineStatusErrs) > 0 {
		errs = append(errs, capiMachineStatusErrs...)
	}

	capiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:            mapiMachine.Name,
			Namespace:       capiNamespace,
			Labels:          convertMAPILabelsToCAPI(mapiMachine.Labels),
			Annotations:     convertMAPIAnnotationsToCAPI(mapiMachine.Annotations),
			Finalizers:      []string{clusterv1.MachineFinalizer},
			OwnerReferences: nil, // OwnerReferences not populated here. They are added later by the machineSync controller.
		},
		Spec: clusterv1.MachineSpec{
			InfrastructureRef: corev1.ObjectReference{
				APIVersion: apiVersion,
				Kind:       kind,
				Name:       mapiMachine.Name,
				Namespace:  capiNamespace,
			},
			ProviderID: mapiMachine.Spec.ProviderID,
			// ClusterName: // ClusterName not populated here. It is added by higher level functions.
			// AuthoritativeAPI: // AuthoritativeAPI not populated here. Ignore as this is part of the conversion mechanism.

			// Version:        // TODO(OCPCLOUD-2714): To be prevented by VAP.
			// ReadinessGates: // TODO(OCPCLOUD-2714): To be prevented by VAP.
			// NodeDrainTimeout:        // TODO(OCPCLOUD-2715): not present on the MAPI API, we should implement them for feature parity.
			// NodeVolumeDetachTimeout: // TODO(OCPCLOUD-2715): not present on the MAPI API, we should implement them for feature parity.
			// NodeDeletionTimeout:     // TODO(OCPCLOUD-2715): not present on the MAPI API, we should implement them for feature parity.
			NodeDeletionTimeout: &metav1.Duration{Duration: time.Second * 10}, // Hardcode it to the CAPI default value until this is implemented in MAPI.
		},
		Status: capiMachineStatus,
	}

	// Node labels in MAPI are stored under .spec.metadata.labels and then propagated down to the node,
	// whereas in CAPI they are stored in the top level .labels and later propagated down to the node.
	setMAPINodeLabelsToCAPINodeLabels(mapiMachine.Spec.ObjectMeta.Labels, capiMachine)

	// Node annotations in MAPI are stored under .spec.metadata.annotations and then propagated down to the node,
	// whereas in CAPI they are stored in the top level .annotations and later propagated down to the node.
	setMAPINodeAnnotationsToCAPINodeAnnotations(mapiMachine.Spec.ObjectMeta.Annotations, capiMachine)

	// LifecycleHooks in MAPI are a special field (.spec.lifecycleHooks),
	// whereas in CAPI they are defined via special annotations.
	setCAPILifecycleHookAnnotations(mapiMachine.Spec.LifecycleHooks, capiMachine)

	errs = append(errs, handleUnsupportedMachineFields(mapiMachine.Spec)...)

	return capiMachine, errs
}

// convertMAPIMachineToCAPIMachineStatus converts a MAPI Machine to CAPI MachineStatus.
func convertMAPIMachineToCAPIMachineStatus(mapiMachine *mapiv1beta1.Machine) (clusterv1.MachineStatus, field.ErrorList) {
	var errs field.ErrorList

	addresses, addressesErr := convertMAPIMachineAddressesToCAPI(mapiMachine.Status.Addresses)
	if len(addressesErr) > 0 {
		errs = append(errs, addressesErr...)
	}

	capiStatus := clusterv1.MachineStatus{
		NodeRef:             mapiMachine.Status.NodeRef,
		LastUpdated:         mapiMachine.Status.LastUpdated,
		Addresses:           addresses,
		Phase:               convertMAPIMachinePhaseToCAPI(mapiMachine.Status.Phase),
		Conditions:          convertMAPIMachineConditionsToCAPIMachineConditions(mapiMachine),
		V1Beta2:             convertMAPIMachineStatusToCAPIMachineV1Beta2Status(mapiMachine),
		FailureReason:       convertMAPIMachineErrorReasonToCAPIFailureReason(mapiMachine.Status.ErrorReason),
		FailureMessage:      convertMAPIMachineErrorMessageToCAPIFailureMessage(mapiMachine.Status.ErrorMessage),
		InfrastructureReady: deriveCAPIInfrastructureReadyFromMAPI(mapiMachine),
		BootstrapReady:      deriveCAPIBootstrapReadyFromMAPI(mapiMachine),

		// MAPI doesn't provide node system info, so we return nil
		// This field is typically populated by the node controller in CAPI
		NodeInfo: nil,

		// Deletion: not present on the MAPI Machine status. // TODO: this is tied to the node draining and volume detaching, implement once those features are implemented in MAPI.

		// DO NOT SET HERE:
		// CertificatesExpiryDate: // not present on the MAPI Machine status. (This value is only set for control plane machines, not necessary for worker machines conversion)
		// ObservedGeneration: // We don't set the observed generation at this stage as it is handled by the machineSync controller.
	}

	// unused fields from MAPI MachineStatus

	// - ProviderStatus: this is provider-specific and handled by separate infrastructure resources in CAPI. // TODO: use this when we implement CAPI InfraMachine conversion.
	// - LastOperation: this is MAPI-specific and not used in CAPI.
	// - AuthoritativeAPI: this is part of the conversion mechanism, it is not used in CAPI.
	// - SynchronizedGeneration: this is part of the conversion mechanism, it is not used in CAPI.

	return capiStatus, errs
}

// convertMAPIMachineStatusToCAPIMachineV1Beta2Status converts a MAPI Machine to CAPI MachineV1Beta2Status.
func convertMAPIMachineStatusToCAPIMachineV1Beta2Status(mapiMachine *mapiv1beta1.Machine) *clusterv1.MachineV1Beta2Status {
	return &clusterv1.MachineV1Beta2Status{
		Conditions: convertMAPIMachineConditionsToCAPIMachineV1Beta2StatusConditions(mapiMachine),
	}
}

// convertMAPIMachineConditionsToCAPIMachineConditions converts MAPI conditions to CAPI v1beta1 conditions.
//
//nolint:funlen
func convertMAPIMachineConditionsToCAPIMachineConditions(mapiMachine *mapiv1beta1.Machine) clusterv1.Conditions {
	capiConditions := []clusterv1.Condition{}

	// According to CAPI v1beta1 machine conditions, there are three main conditions:
	// Ready, BootstrapReady, InfrastructureReady

	readyCondition := clusterv1.Condition{
		Type: clusterv1.ReadyCondition,
		Status: func() corev1.ConditionStatus {
			if mapiMachine.Status.Phase != nil && *mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		Severity: func() clusterv1.ConditionSeverity {
			if mapiMachine.Status.Phase != nil && *mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning {
				return clusterv1.ConditionSeverityNone
			}

			return clusterv1.ConditionSeverityError
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	bootstrapReadyCondition := clusterv1.Condition{
		Type: clusterv1.BootstrapReadyCondition,
		Status: func() corev1.ConditionStatus {
			if deriveCAPIBootstrapReadyFromMAPI(mapiMachine) {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		Severity: func() clusterv1.ConditionSeverity {
			if !deriveCAPIBootstrapReadyFromMAPI(mapiMachine) {
				return clusterv1.ConditionSeverityInfo
			}

			return clusterv1.ConditionSeverityNone
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	infrastructureReadyCondition := clusterv1.Condition{
		Type: clusterv1.InfrastructureReadyCondition,
		Status: func() corev1.ConditionStatus {
			if deriveCAPIInfrastructureReadyFromMAPI(mapiMachine) {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		Reason: func() string {
			if !deriveCAPIInfrastructureReadyFromMAPI(mapiMachine) {
				return clusterv1.WaitingForInfrastructureFallbackReason
			}

			return ""
		}(),
		Severity: func() clusterv1.ConditionSeverity {
			if !deriveCAPIInfrastructureReadyFromMAPI(mapiMachine) {
				return clusterv1.ConditionSeverityInfo
			}

			return clusterv1.ConditionSeverityNone
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	capiConditions = append(capiConditions, readyCondition, bootstrapReadyCondition, infrastructureReadyCondition)

	return capiConditions
}

// convertMAPIMachineConditionsToCAPIMachineV1Beta2StatusConditions converts MAPI conditions to CAPI v1beta2 conditions.
//
//nolint:funlen
func convertMAPIMachineConditionsToCAPIMachineV1Beta2StatusConditions(mapiMachine *mapiv1beta1.Machine) []metav1.Condition {
	capiConditions := []metav1.Condition{}

	// According to CAPI v1beta2 machine conditions, there are 9 main conditions:
	// Available, Ready, UpToDate, BootstrapConfigReady, InfrastructureReady, NodeReady, NodeHealthy, Deleting, Paused

	// Available condition - indicates if the machine is available for use
	availableCondition := metav1.Condition{
		Type: clusterv1.AvailableV1Beta2Condition,
		Status: func() metav1.ConditionStatus {
			if hasRunningPhase(mapiMachine) {
				return metav1.ConditionTrue
			}

			return metav1.ConditionFalse
		}(),
		Reason: func() string {
			if hasRunningPhase(mapiMachine) {
				return clusterv1.MachineAvailableV1Beta2Reason // This is "Available"
			}

			return clusterv1.NotAvailableV1Beta2Reason // This is "NotAvailable"
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// Ready condition
	readyCondition := metav1.Condition{
		Type: clusterv1.ReadyV1Beta2Condition,
		Status: func() metav1.ConditionStatus {
			if mapiMachine.Status.Phase != nil && *mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning {
				return metav1.ConditionTrue
			}

			return metav1.ConditionFalse
		}(),
		Reason: func() string {
			if mapiMachine.Status.Phase != nil && *mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning {
				return clusterv1.MachineReadyV1Beta2Reason
			}

			return clusterv1.MachineNotReadyV1Beta2Reason
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// BootstrapConfigReady condition
	bootstrapConfigReadyCondition := metav1.Condition{
		Type: clusterv1.MachineBootstrapConfigReadyV1Beta2Condition,
		Status: func() metav1.ConditionStatus {
			if deriveCAPIBootstrapReadyFromMAPI(mapiMachine) {
				return metav1.ConditionTrue
			}

			return metav1.ConditionFalse
		}(),
		Reason: func() string {
			if deriveCAPIBootstrapReadyFromMAPI(mapiMachine) {
				return clusterv1.MachineBootstrapConfigReadyV1Beta2Reason
			}

			return clusterv1.MachineBootstrapConfigNotReadyV1Beta2Reason
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// InfrastructureReady condition
	infrastructureReadyCondition := metav1.Condition{
		Type: clusterv1.MachineInfrastructureReadyV1Beta2Condition,
		Status: func() metav1.ConditionStatus {
			if deriveCAPIInfrastructureReadyFromMAPI(mapiMachine) {
				return metav1.ConditionTrue
			}

			return metav1.ConditionFalse
		}(),
		Reason: func() string {
			if deriveCAPIInfrastructureReadyFromMAPI(mapiMachine) {
				return clusterv1.MachineInfrastructureReadyV1Beta2Reason
			}

			return clusterv1.MachineInfrastructureNotReadyV1Beta2Reason
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// NodeReady condition
	// For now use the machine phase to determine the status of the node ready condition.
	// TODO: update this if we change our mind for the nodehealthy condition below.
	nodeReadyCondition := metav1.Condition{
		Type: clusterv1.MachineNodeReadyV1Beta2Condition,
		Status: func() metav1.ConditionStatus {
			if mapiMachine.Status.Phase != nil && (*mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning || *mapiMachine.Status.Phase == mapiv1beta1.PhaseDeleting) && mapiMachine.Status.NodeRef != nil {
				return metav1.ConditionTrue
			}

			return metav1.ConditionFalse
		}(),
		Reason: func() string {
			if mapiMachine.Status.Phase != nil && (*mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning || *mapiMachine.Status.Phase == mapiv1beta1.PhaseDeleting) && mapiMachine.Status.NodeRef != nil {
				return clusterv1.MachineNodeReadyV1Beta2Reason
			}

			return clusterv1.MachineNodeNotReadyV1Beta2Reason
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// NodeHealthy condition
	// MachineNodeHealthyV1Beta2Condition is true if the Machine's Node is ready and it does not report MemoryPressure, DiskPressure and PIDPressure.
	// We don't ned this at the moment, and tt require significant hoops to get the Node object everytime and pipe it down to here.
	// Do not implement this for now, rationale:
	// https://github.com/openshift/cluster-capi-operator/pull/365#discussion_r2378857251

	// UpToDate condition
	// We should never set this condition in CAPI because we don't use MachineDeployments on the MAPI side
	// and/or don't support "matching" higher level abstractions for the conversion of a MachineSet from MAPI to CAPI

	// Paused condition
	// We ignore paused condition at this level as it is handled by the machineSetMigration controller.

	// Deleting condition
	isDeleting := mapiMachine.DeletionTimestamp != nil && !mapiMachine.DeletionTimestamp.IsZero()
	deletingCondition := metav1.Condition{
		Type:   clusterv1.MachineDeletingV1Beta2Condition,
		Status: map[bool]metav1.ConditionStatus{true: metav1.ConditionTrue, false: metav1.ConditionFalse}[isDeleting],
		Reason: map[bool]string{true: clusterv1.MachineDeletingV1Beta2Reason, false: clusterv1.MachineNotDeletingV1Beta2Reason}[isDeleting],
		// LastTransitionTime will be set by the condition utilities.
	}

	capiConditions = append(capiConditions, availableCondition, readyCondition, bootstrapConfigReadyCondition, infrastructureReadyCondition, deletingCondition, nodeReadyCondition)

	return capiConditions
}

// convertMAPIMachineAddressesToCAPI converts MAPI machine addresses to CAPI format.
func convertMAPIMachineAddressesToCAPI(mapiAddresses []corev1.NodeAddress) (clusterv1.MachineAddresses, field.ErrorList) {
	if mapiAddresses == nil {
		return nil, nil
	}

	errs := field.ErrorList{}
	capiAddresses := make(clusterv1.MachineAddresses, 0, len(mapiAddresses))

	// Addresses are slightly different between MAPI/CAPI.
	// In CAPI the address type can be: Hostname, ExternalIP, InternalIP, ExternalDNS or InternalDNS
	// In MAPI the address type can be: Hostname, ExternalIP, InternalIP (missing ExternalDNS and InternalDNS)
	// This is fine when going from MAPI to CAPI, but needs to be handled when going from CAPI to MAPI.
	for _, addr := range mapiAddresses {
		var t clusterv1.MachineAddressType

		switch addr.Type {
		case corev1.NodeHostName:
			t = clusterv1.MachineHostName
		case corev1.NodeExternalIP:
			t = clusterv1.MachineExternalIP
		case corev1.NodeInternalIP:
			t = clusterv1.MachineInternalIP
		case corev1.NodeExternalDNS:
			t = clusterv1.MachineExternalDNS
		case corev1.NodeInternalDNS:
			t = clusterv1.MachineInternalDNS
		default:
			errs = append(errs, field.Invalid(field.NewPath("status", "addresses"), string(addr.Type), string(addr.Type)+" unrecognized address type"))

			// Ignore the address if the type is unrecognized.
			continue
		}

		capiAddresses = append(capiAddresses, clusterv1.MachineAddress{
			Type:    t,
			Address: addr.Address,
		})
	}

	return capiAddresses, errs
}

// convertMAPIMachinePhaseToCAPI converts MAPI machine phase to CAPI format.
func convertMAPIMachinePhaseToCAPI(mapiPhase *string) string {
	// Phase is slightly different between MAPI/CAPI.
	// In CAPI can be one of: Pending;Provisioning;Provisioned;Running;Deleting;Deleted;Failed;Unknown
	// In MAPI can be one of: Provisioning;Provisioned;Running;Deleting;Failed (missing Pending,Unknown)
	// This is fine when going from MAPI to CAPI, but needs to be handled when going from CAPI to MAPI.
	// MAPI and CAPI phases are compatible, but we need to handle the pointer vs string difference
	return ptr.Deref(mapiPhase, "")
}

// convertMAPIMachineErrorReasonToCAPIFailureReason converts MAPI MachineStatusError to CAPI MachineStatusError.
func convertMAPIMachineErrorReasonToCAPIFailureReason(mapiErrorReason *mapiv1beta1.MachineStatusError) *capierrors.MachineStatusError {
	if mapiErrorReason == nil {
		return nil
	}

	return ptr.To(capierrors.MachineStatusError(*mapiErrorReason))
}

// convertMAPIMachineErrorMessageToCAPIFailureMessage converts MAPI MachineStatusError to CAPI MachineStatusError.
func convertMAPIMachineErrorMessageToCAPIFailureMessage(mapiErrorMessage *string) *string {
	return mapiErrorMessage
}

// deriveCAPIBootstrapReadyFromMAPI derives the CAPI BootstrapReady field from MAPI machine state.
func deriveCAPIBootstrapReadyFromMAPI(mapiMachine *mapiv1beta1.Machine) bool {
	// Bootstrap is considered ready if the machine is in Running, Deleting phases
	if mapiMachine.Status.Phase != nil {
		phase := *mapiMachine.Status.Phase

		return phase == mapiv1beta1.PhaseRunning || phase == mapiv1beta1.PhaseDeleting
	}

	return false
}

// deriveCAPIInfrastructureReadyFromMAPI derives the CAPI InfrastructureReady field from MAPI machine state.
func deriveCAPIInfrastructureReadyFromMAPI(mapiMachine *mapiv1beta1.Machine) bool {
	// Infrastructure is considered ready if the machine is in Provisioned, Running, Deleting phases
	if mapiMachine.Status.Phase != nil {
		phase := *mapiMachine.Status.Phase
		return phase == mapiv1beta1.PhaseProvisioned || phase == mapiv1beta1.PhaseRunning || phase == mapiv1beta1.PhaseDeleting
	}

	return false
}

// hasRunningPhase checks if the machine is in the Running phase.
func hasRunningPhase(mapiMachine *mapiv1beta1.Machine) bool {
	return mapiMachine.Status.Phase != nil && *mapiMachine.Status.Phase == mapiv1beta1.PhaseRunning
}
