/*
Copyright 2022 Red Hat, Inc.

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

package util

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// machineRoleLabelName is the label used to identify the role of a machine.
	machineRoleLabelName = "machine.openshift.io/cluster-api-machine-role"

	// machineTypeLabelName is the label used to identify the type of a machine.
	machineTypeLabelName = "machine.openshift.io/cluster-api-machine-type"

	// nodeRoleMasterLabelName is the label used to identify the role of the node.
	nodeRoleMasterLabelName = "node-role.kubernetes.io/master"

	// nodeRoleControlPlaneLabelName is the label used to identify the role of the node.
	nodeRoleControlPlaneLabelName = "node-role.kubernetes.io/control-plane"

	// machineMasterRoleLabelName is the label value to identify the role of a control plane machine.
	machineMasterRoleLabelName = "master"

	// machineMasterTypeLabelName is the label value to identify the type of a control plane machine.
	machineMasterTypeLabelName = "master"

	// machineControlPlaneTypeLabelName is the label value to identify the type of a control plane machine.
	machineControlPlaneTypeLabelName = "control-plane"
)

// ObjToControlPlaneMachineSet maps any object to the control plane machine set singleton
// in the namespace provided.
func ObjToControlPlaneMachineSet(controlPlaneMachineSetName, namespace string) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		klog.V(4).Info(
			"reconcile triggered by object",
			"objectType", fmt.Sprintf("%T", obj),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName(),
		)

		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Namespace: namespace, Name: controlPlaneMachineSetName},
		}}
	}
}

// FilterClusterOperator filters cluster operator requests
// to just the one with the name provided.
func FilterClusterOperator(name string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		co, ok := obj.(*configv1.ClusterOperator)
		if !ok {
			panic("expected to get an of object of type configv1.ClusterOperator")
		}

		return co.GetName() == name
	})
}

// FilterControlPlaneMachineSet filters control plane machine set requests
// to just the singleton within the namespace provided.
func FilterControlPlaneMachineSet(controlPlaneMachineSetName, namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		cpms, ok := obj.(*machinev1.ControlPlaneMachineSet)
		if !ok {
			panic("expected to get an of object of type machinev1.ControlPlaneMachineSet")
		}

		shouldReconcile := cpms.GetNamespace() == namespace && cpms.GetName() == controlPlaneMachineSetName

		if shouldReconcile {
			klog.V(4).Info(
				"reconcile triggered by control plane machine set",
				"namespace", obj.GetNamespace(),
				"name", obj.GetName(),
			)
		}

		return shouldReconcile
	})
}

// FilterControlPlaneMachines filters machine requests to just the machines that present as control plane machines,
// i.e. they are labelled with the correct labels to identify them as control plane machines.
func FilterControlPlaneMachines(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		machine, ok := obj.(*machinev1beta1.Machine)
		if !ok {
			panic(fmt.Sprintf("expected to get an of object of type machinev1beta1.Machine: got type %T", obj))
		}

		// Check namespace first
		if machine.GetNamespace() != namespace {
			return false
		}

		// Ensuring that this is a master machine by checking required labels
		labels := machine.GetLabels()

		return labels[machineRoleLabelName] == machineMasterRoleLabelName &&
			(labels[machineTypeLabelName] == machineMasterTypeLabelName ||
				labels[machineTypeLabelName] == machineControlPlaneTypeLabelName)
	})
}

// FilterControlPlaneNodes filters nodes requests to just the nodes that present as control plane nodes
// and that have had a transition in the NodeReady condition.
func FilterControlPlaneNodes() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			node, ok := e.Object.(*corev1.Node)
			if !ok {
				panic(fmt.Sprintf("expected to get an of object of type corev1.Node: got type %T", e.Object))
			}

			return isControlPlaneNode(node)
		},
		UpdateFunc: updateFuncFilterControlPlaneNodes,
		DeleteFunc: func(e event.DeleteEvent) bool {
			node, ok := e.Object.(*corev1.Node)
			if !ok {
				panic(fmt.Sprintf("expected to get an of object of type corev1.Node: got type %T", e.Object))
			}

			return isControlPlaneNode(node)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			if _, ok := e.Object.(*corev1.Node); !ok {
				panic(fmt.Sprintf("expected to get an of object of type corev1.Node: got type %T", e.Object))
			}

			// We are only interested in events that can change a Node status,
			// so we ignore generic events.
			return false
		},
	}
}

// FilterInfrastructure filters out responding to any Infrastructure object that is not the one specified.
func FilterInfrastructure(infraName string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		infra, ok := obj.(*configv1.Infrastructure)
		if !ok {
			panic("expected to get an of object of type configv1.infrastructure")
		}

		shouldReconcile := infra.GetName() == infraName

		if shouldReconcile {
			klog.V(2).Info("reconcile triggered by infrastructure change")
		}

		return shouldReconcile
	})
}

// isControlPlaneNode checks whether the provided node is a control plane one.
func isControlPlaneNode(node *corev1.Node) bool {
	// Ensuring that this is a master machine by checking required labels.
	labels := node.GetLabels()
	_, hasMasterLabel := labels[nodeRoleMasterLabelName]
	_, hasControlPlaneLabel := labels[nodeRoleControlPlaneLabelName]

	// Only consider if Node is a control plane.
	return hasMasterLabel || hasControlPlaneLabel
}

// updateFuncFilterControlPlaneNodes filters an event based on whether the node is a control plane
// or not and if it has recently transitioned in its readiness status.
func updateFuncFilterControlPlaneNodes(e event.UpdateEvent) bool {
	node, ok := e.ObjectNew.(*corev1.Node)
	if !ok {
		panic(fmt.Sprintf("expected to get an of object of type corev1.Node: got type %T", e.ObjectNew))
	}

	oldNode, ok := e.ObjectOld.(*corev1.Node)
	if !ok && oldNode != nil {
		panic(fmt.Sprintf("expected to get an of object of type corev1.Node: got type %T", e.ObjectOld))
	}

	if !isControlPlaneNode(node) {
		return false
	}

	var wasNodeReady, isNodeReady corev1.ConditionStatus

	for _, c := range oldNode.Status.Conditions {
		if c.Type == corev1.NodeReady {
			wasNodeReady = c.Status
			break
		}
	}

	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			isNodeReady = c.Status
			break
		}
	}

	// Only consider if the node has changed in its readiness.
	return wasNodeReady != isNodeReady
}
