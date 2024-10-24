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

package machinesync

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	awscapiv1beta1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta1"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	capiNamespace  string = "openshift-cluster-api"
	mapiNamespace  string = "openshift-machine-api"
	machineSetKind string = "MachineSet"
	controllerName string = "MachineSyncController"
)

var (
	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachine type, platform not supported")
)

// MachineSyncReconciler reconciles CAPI and MAPI machines.
type MachineSyncReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets the CoreClusterReconciler controller up with the given manager.
func (r *MachineSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	infraMachine, err := getInfraMachineFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get InfraMachine from Provider: %w", err)
	}

	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = capiNamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = mapiNamespace
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&machinev1beta1.Machine{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&capiv1beta1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Watches(
			infraMachine,
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	// Set up API helpers from the manager.
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor("machine-sync-controller")

	return nil
}

// Reconcile reconciles CAPI and MAPI machines for their respective namespaces.
//
//nolint:funlen
func (r *MachineSyncReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "namespace", req.Namespace, "name", req.Name)

	logger.V(1).Info("Reconciling machine")
	defer logger.V(1).Info("Finished reconciling machine")

	var mapiMachineNotFound, capiMachineNotFound bool

	// Get the MAPI Machine.
	mapiMachine := &machinev1beta1.Machine{}
	mapiNamespacedName := client.ObjectKey{
		Namespace: r.MAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, mapiNamespacedName, mapiMachine); apierrors.IsNotFound(err) {
		logger.Info("MAPI Machine not found")

		mapiMachineNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get MAPI Machine")
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine: %w", err)
	}

	// Get the corresponding CAPI Machine.
	capiMachine := &capiv1beta1.Machine{}
	capiNamespacedName := client.ObjectKey{
		Namespace: r.CAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, capiNamespacedName, capiMachine); apierrors.IsNotFound(err) {
		logger.Info("CAPI Machine not found")

		capiMachineNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get CAPI Machine")
		return ctrl.Result{}, fmt.Errorf("failed to get CAPI machine:: %w", err)
	}

	if mapiMachineNotFound && capiMachineNotFound {
		logger.Info("CAPI and MAPI machines not found, nothing to do")
		return ctrl.Result{}, nil
	}

	// We mirror if the CAPI machine is owned by a MachineSet which has a MAPI
	// counterpart. This is because we want to be able to migrate in both directions.
	if mapiMachineNotFound {
		if shouldReconcile, err := r.shouldMirrorCAPIMachineToMAPIMachine(ctx, logger, capiMachine); err != nil {
			return ctrl.Result{}, err
		} else if shouldReconcile {
			return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
		}
	}

	switch mapiMachine.Status.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachinetoCAPIMachine(ctx, mapiMachine, capiMachine)
	case machinev1beta1.MachineAuthorityClusterAPI:
		return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
	case machinev1beta1.MachineAuthorityMigrating:
		logger.Info("machine currently migrating", "machine", mapiMachine.GetName())
		return ctrl.Result{}, nil
	default:
		logger.Info("machine AuthoritativeAPI has unexpected value", "AuthoritativeAPI", mapiMachine.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	}
}

// reconcileCAPIMachinetoMAPIMachine reconciles a CAPI Machine to a MAPI Machine.
func (r *MachineSyncReconciler) reconcileCAPIMachinetoMAPIMachine(ctx context.Context, capiMachine *capiv1beta1.Machine, mapiMachine *machinev1beta1.Machine) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// reconcileMAPIMachinetoCAPIMachine a MAPI Machine to a CAPI Machine.
func (r *MachineSyncReconciler) reconcileMAPIMachinetoCAPIMachine(ctx context.Context, mapiMachine *machinev1beta1.Machine, capiMachine *capiv1beta1.Machine) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// getInfraMachineFromProvider returns the correct InfraMachine implementation
// for a given provider.
//
// As we implement other cloud providers, we'll need to update this list.
func getInfraMachineFromProvider(platform configv1.PlatformType) (client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &awscapiv1beta1.AWSMachine{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachine{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// shouldMirrorCAPIMachineToMAPIMachine takes a CAPI machine and determines if there should
// be a MAPI mirror, it returns true only if:
//
// 1. The CAPI Machine is owned by a CAPI MachineSet,
// 2. That owning CAPI MachineSet has a MAPI MachineSet Mirror.
func (r *MachineSyncReconciler) shouldMirrorCAPIMachineToMAPIMachine(ctx context.Context, logger logr.Logger, machine *capiv1beta1.Machine) (bool, error) {
	logger.WithName("shouldMirrorCAPIMachineToMAPIMachine").
		Info("checking if CAPI machine should be mirrored", "machine", machine.GetName())

	// Check if owner refs point to CAPI MachineSet
	for _, ref := range machine.ObjectMeta.OwnerReferences {
		if ref.Kind != machineSetKind || ref.APIVersion != capiv1beta1.GroupVersion.String() {
			continue
		}

		logger.Info("CAPI machine is owned by a machineset",
			"machine", machine.GetName(), "machineset", ref.Name)
		// Checks if the CAPI MS has a mirror in MAPI namespace
		key := client.ObjectKey{
			Namespace: r.MAPINamespace,
			Name:      ref.Name,
		}
		mapiMachineSet := &machinev1beta1.MachineSet{}

		if err := r.Get(ctx, key, mapiMachineSet); apierrors.IsNotFound(err) {
			logger.Info("MAPI MachineSet mirror not found, nothing to do",
				"machine", machine.GetName(), "machineset", ref.Name)

			return false, nil
		} else if err != nil {
			logger.Error(err, "Failed to get MAPI MachineSet mirror")

			return false, fmt.Errorf("failed to get MAPI MachineSet: %w", err)
		}

		return true, nil
	}

	logger.Info("CAPI machine is not owned by a machineset, nothing to do", "machine", machine.GetName())

	return false, nil
}
