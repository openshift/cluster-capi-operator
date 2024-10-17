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

package machinesetsync

import (
	"context"
	"errors"
	"fmt"

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
	capiNamespace string = "openshift-cluster-api"
	mapiNamespace string = "openshift-machine-api"
)

var (
	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachineTemplate type, platform not supported")
)

// MachineSetSyncReconciler reconciles CAPI and MAPI MachineSets.
type MachineSetSyncReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets the CoreClusterReconciler controller up with the given manager.
func (r *MachineSetSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	infraMachineTemplate, err := getInfraMachineTemplateFromProvider(r.Platform)
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
		For(&machinev1beta1.MachineSet{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&capiv1beta1.MachineSet{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Watches(
			infraMachineTemplate,
			handler.EnqueueRequestsFromMapFunc(util.ResolveCAPIMachineSetFromObject(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	// Set up API helpers from the manager.
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor("machineset-sync-controller")

	return nil
}

// Reconcile reconciles CAPI and MAPI machines for their respective namespaces.
//
//nolint:funlen
func (r *MachineSetSyncReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "namespace", req.Namespace, "name", req.Name)

	logger.V(1).Info("Reconciling machineset")
	defer logger.V(1).Info("Finished reconciling machineset")

	var mapiMachineSetNotFound, capiMachineSetNotFound bool

	// Get the MAPI MachineSet.
	mapiMachineSet := &machinev1beta1.MachineSet{}
	mapiNamespacedName := client.ObjectKey{
		Namespace: r.MAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, mapiNamespacedName, mapiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("MAPI MachineSet not found")

		mapiMachineSetNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get MAPI MachineSet")
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI MachineSet: %w", err)
	}

	// Get the corresponding CAPI Machine.
	capiMachineSet := &capiv1beta1.MachineSet{}
	capiNamespacedName := client.ObjectKey{
		Namespace: r.CAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, capiNamespacedName, capiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("CAPI MachineSet not found")

		capiMachineSetNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get CAPI MachineSet")
		return ctrl.Result{}, fmt.Errorf("failed to get CAPI MachineSet: %w", err)
	}

	if mapiMachineSetNotFound && capiMachineSetNotFound {
		logger.Info("CAPI and MAPI MachineSets not found, nothing to do")
		return ctrl.Result{}, nil
	}

	// If the MachineSet only exists in CAPI, we don't need to sync back to MAPI.
	if mapiMachineSetNotFound {
		logger.Info("Only CAPI MachineSet found, nothing to do")
		return ctrl.Result{}, nil
	}

	switch mapiMachineSet.Status.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachineSettoCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet)
	case machinev1beta1.MachineAuthorityClusterAPI:
		return r.reconcileCAPIMachineSettoMAPIMachineSet(ctx, capiMachineSet, mapiMachineSet)
	case machinev1beta1.MachineAuthorityMigrating:
		logger.Info("machine currently migrating", "machine", mapiMachineSet.GetName())
		return ctrl.Result{}, nil
	default:
		logger.Info("machine AuthoritativeAPI has unexpected value", "AuthoritativeAPI", mapiMachineSet.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	}
}

// reconcileCAPIMachineSettoMAPIMachineSet reconciles a CAPI MachineSet to a MAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileCAPIMachineSettoMAPIMachineSet(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// reconcileMAPIMachineSettoCAPIMachineSet MAPI MachineSet to a CAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileMAPIMachineSettoCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// getInfraMachineTemplateFromProvider returns the correct InfraMachineTemplate implementation
// for a given provider.
//
// As we implement other cloud providers, we'll need to update this list.
func getInfraMachineTemplateFromProvider(platform configv1.PlatformType) (client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &awscapiv1beta1.AWSMachineTemplate{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachineTemplate{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}
