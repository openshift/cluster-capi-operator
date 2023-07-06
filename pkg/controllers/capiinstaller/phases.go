/*
Copyright 2022 The Kubernetes Authors.

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
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/cluster"
	configclient "sigs.k8s.io/cluster-api/cmd/clusterctl/client/config"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/repository"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	metadataFile = "metadata.yaml"
	coreProvider = "CoreProvider"
	// preflightFailedRequeueAfter is how long to wait before trying to reconcile
	// if some preflight check has failed.
	preflightFailedRequeueAfter = 30 * time.Second
)

var (
	moreThanOneCoreProviderInstanceExistsMessage = "CoreProvider already exists in the cluster. Only one is allowed."
	moreThanOneProviderInstanceExistsMessage     = "There is already a %s with name %s in the cluster. Only one is allowed."
	capiVersionIncompatibilityMessage            = "capi operator is only compatible with %s providers, detected %s for provider %s."
	waitingForCoreProviderReadyMessage           = "waiting for the core provider to install."
	emptyVersionMessage                          = "version cannot be empty"
)

type provider struct {
	Name      string
	Namespace string
	Version   string
}

// phaseReconciler holds all required information for interacting with clusterctl code and
// helps to iterate through provider reconciliation phases.
type phaseReconciler struct {
	provider provider

	ctrlClient         client.Client
	ctrlConfig         *rest.Config
	repo               repository.Repository
	images             map[string]string
	contract           string
	options            repository.ComponentsOptions
	providerConfig     configclient.Provider
	configClient       configclient.Client
	components         repository.Components
	clusterctlProvider *clusterctlv1.Provider
}

// reconcilePhaseFn is a function that represent a phase of the reconciliation.
type reconcilePhaseFn func(context.Context) (reconcile.Result, error)

// PhaseError custom error type for phases.
type PhaseError struct {
	Reason   string
	Type     clusterv1.ConditionType
	Severity clusterv1.ConditionSeverity
	Err      error
}

func (p *PhaseError) Error() string {
	return p.Err.Error()
}

func wrapPhaseError(err error, reason string, ctype clusterv1.ConditionType) error {
	if err == nil {
		return nil
	}
	return &PhaseError{
		Err:      err,
		Type:     ctype,
		Reason:   reason,
		Severity: clusterv1.ConditionSeverityWarning,
	}
}

// newPhaseReconciler returns phase reconciler for the given provider.
func newPhaseReconciler(r CapiInstallerController, provider provider) *phaseReconciler {
	return &phaseReconciler{
		ctrlClient:         r.Client,
		ctrlConfig:         r.RestCfg,
		clusterctlProvider: &clusterctlv1.Provider{},
		provider:           provider,
		images:             r.Images,
	}
}

// preflightChecks a wrapper around the preflight checks.
func (p *phaseReconciler) preflightChecks(ctx context.Context) (reconcile.Result, error) {
	return preflightChecks(ctx, p.ctrlClient, p.provider)
}

// load provider specific configuration into phaseReconciler object.
func (p *phaseReconciler) load(ctx context.Context) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	log.V(2).Info("Loading provider", "name", p.provider.Name)

	// Initialize the a client for interacting with the clusterctl configuration.
	var err error
	p.configClient, err = configclient.New("")
	if err != nil {
		return reconcile.Result{}, err
	}

	// Get returns the configuration for the provider with a given name/type.
	// This is done using clusterctl internal API types.
	p.providerConfig, err = p.configClient.Providers().Get(p.provider.Name, clusterctlProviderType(p.provider))
	if err != nil {
		return reconcile.Result{}, wrapPhaseError(err, operatorv1.UnknownProviderReason, operatorv1.PreflightCheckCondition)
	}

	p.repo, err = p.configmapRepository(ctx)
	if err != nil {
		return reconcile.Result{}, wrapPhaseError(err, "failed to load the repository", operatorv1.PreflightCheckCondition)
	}

	// Store some provider specific inputs for passing it to clusterctl library
	p.options = repository.ComponentsOptions{
		TargetNamespace:     p.provider.Namespace,
		SkipTemplateProcess: false,
		Version:             p.provider.Version,
	}

	if err := p.validateRepoCAPIVersion(); err != nil {
		return reconcile.Result{}, wrapPhaseError(err, operatorv1.CAPIVersionIncompatibilityReason, operatorv1.PreflightCheckCondition)
	}

	return reconcile.Result{}, nil
}

// configmapRepository use clusterctl NewMemoryRepository structure to store the manifests
// and metadata from a given configmap.
func (p *phaseReconciler) configmapRepository(ctx context.Context) (repository.Repository, error) {
	mr := repository.NewMemoryRepository()
	mr.WithPaths("", "components.yaml")

	cml := &corev1.ConfigMapList{}
	if err := p.ctrlClient.List(ctx, cml, client.HasLabels{configMapVersionLabelName}); err != nil {
		return nil, err
	}
	if len(cml.Items) == 0 {
		return nil, fmt.Errorf("no ConfigMaps found with selector key %q", configMapVersionLabelName)
	}

	for _, cm := range cml.Items {
		version := cm.Name
		errMsg := "from the Name"
		if cm.Labels != nil {
			ver, ok := cm.Labels[operatorv1.ConfigMapVersionLabelName]
			if ok {
				version = ver
				errMsg = "from the Label " + operatorv1.ConfigMapVersionLabelName
			}
		}

		if _, err := versionutil.ParseSemantic(version); err != nil {
			return nil, fmt.Errorf("ConfigMap %s/%s has invalid version:%s (%s)", cm.Namespace, cm.Name, version, errMsg)
		}

		metadata, ok := cm.Data["metadata"]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %s/%s has no metadata", cm.Namespace, cm.Name)
		}
		mr.WithFile(version, metadataFile, []byte(metadata))

		components, ok := cm.Data["components"]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %s/%s has no components", cm.Namespace, cm.Name)
		}
		mr.WithFile(version, mr.ComponentsPath(), []byte(components))
	}

	return mr, nil
}

// validateRepoCAPIVersion checks that the repo is using the correct version.
func (p *phaseReconciler) validateRepoCAPIVersion() error {
	name := p.provider.Name
	file, err := p.repo.GetFile(p.options.Version, metadataFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read %q from the repository for provider %q", metadataFile, name)
	}

	// Convert the yaml into a typed object
	latestMetadata := &clusterctlv1.Metadata{}
	codecFactory := serializer.NewCodecFactory(scheme.Scheme)

	if err := runtime.DecodeInto(codecFactory.UniversalDecoder(), file, latestMetadata); err != nil {
		return errors.Wrapf(err, "error decoding %q for provider %q", metadataFile, name)
	}

	// Gets the contract for the target release.
	targetVersion, err := versionutil.ParseSemantic(p.options.Version)
	if err != nil {
		return errors.Wrapf(err, "failed to parse current version for the %s provider", name)
	}

	releaseSeries := latestMetadata.GetReleaseSeriesForVersion(targetVersion)
	if releaseSeries == nil {
		return errors.Errorf("invalid provider metadata: version %s for the provider %s does not match any release series", p.options.Version, name)
	}
	if releaseSeries.Contract != "v1alpha4" && releaseSeries.Contract != "v1beta1" {
		return errors.Errorf(capiVersionIncompatibilityMessage, clusterv1.GroupVersion.Version, releaseSeries.Contract, name)
	}
	p.contract = releaseSeries.Contract
	return nil
}

// fetch fetches the provider components from the repository and processes all yaml manifests.
func (p *phaseReconciler) fetch(ctx context.Context) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Fetching provider", "name", p.provider.Name)

	// Fetch the provider components yaml file from the provided repository Github/ConfigMap.
	componentsFile, err := p.repo.GetFile(p.options.Version, p.repo.ComponentsPath())
	if err != nil {
		err = fmt.Errorf("failed to read %q from provider's repository %q: %w", p.repo.ComponentsPath(), p.providerConfig.ManifestLabel(), err)
		return reconcile.Result{}, wrapPhaseError(err, operatorv1.ComponentsFetchErrorReason, operatorv1.PreflightCheckCondition)
	}

	// Generate a set of new objects using the clusterctl library. NewComponents() will do the yaml proccessing,
	// like ensure all the provider components are in proper namespace, replcae variables, etc. See the clusterctl
	// documentation for more details.
	p.components, err = repository.NewComponents(repository.ComponentsInput{
		Provider:     p.providerConfig,
		ConfigClient: p.configClient,
		Processor:    yamlprocessor.NewSimpleProcessor(),
		RawYaml:      componentsFile,
		Options:      p.options,
	})
	if err != nil {
		return reconcile.Result{}, wrapPhaseError(err, operatorv1.ComponentsFetchErrorReason, operatorv1.PreflightCheckCondition)
	}

	// ProviderSpec provides fields for customizing the provider deployment options.
	// We can use clusterctl library to apply this customizations.
	err = repository.AlterComponents(p.components, customizeObjectsFn(p.provider, p.images))
	if err != nil {
		return reconcile.Result{}, wrapPhaseError(err, operatorv1.ComponentsFetchErrorReason, operatorv1.PreflightCheckCondition)
	}

	// conditions.Set(p.provider, conditions.TrueCondition(operatorv1.PreflightCheckCondition))
	return reconcile.Result{}, nil
}

// preInstall ensure all the clusterctl CRDs are available before installing the provider,
// and delete existing components if required for upgrade.
func (p *phaseReconciler) preInstall(ctx context.Context) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	clusterClient := p.newClusterClient()

	log.V(2).Info("Ensuring clustectl CRDs are installed", "name", p.provider.Name)
	err := clusterClient.ProviderInventory().EnsureCustomResourceDefinitions()
	if err != nil {
		return reconcile.Result{}, wrapPhaseError(err, "failed installing clusterctl CRDs", operatorv1.ProviderInstalledCondition)
	}

	// TODO
	// needPreDelete, err := p.updateRequiresPreDeletion(ctx, p.provider)
	// if err != nil || !needPreDelete {
	// 	return reconcile.Result{}, wrapPhaseError(err, "failed getting clusterctl Provider", operatorv1.ProviderInstalledCondition)
	// }

	log.V(1).Info("Upgrade detected, deleting existing components", "name", p.provider.Name)
	return p.delete(ctx)
}

// // updateRequiresPreDeletion try to get installed version from provider status and decide if it's an upgrade.
// func (s *phaseReconciler) updateRequiresPreDeletion(ctx context.Context, provider genericprovider.GenericProvider) (bool, error) {
// 	installedVersion := s.provider.GetStatus().InstalledVersion
// 	if installedVersion == nil {
// 		return false, nil
// 	}

// 	nextVersion, err := versionutil.ParseSemantic(s.components.Version())
// 	if err != nil {
// 		return false, err
// 	}

// 	currentVersion, err := versionutil.ParseSemantic(*installedVersion)
// 	if err != nil {
// 		return false, err
// 	}

// 	return currentVersion.LessThan(nextVersion), nil
// }

// install installs the provider components using clusterctl library.
func (p *phaseReconciler) install(ctx context.Context) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	clusterClient := p.newClusterClient()
	installer := clusterClient.ProviderInstaller()
	installer.Add(p.components)

	log.V(1).Info("Installing provider", "name", p.provider.Name)

	if _, err := installer.Install(cluster.InstallOptions{}); err != nil {
		reason := "Install failed"
		if err == wait.ErrWaitTimeout {
			reason = "Timedout waiting for deployment to become ready"
		}
		return reconcile.Result{}, wrapPhaseError(err, reason, operatorv1.ProviderInstalledCondition)
	}

	// status := p.provider.GetStatus()
	// status.Contract = &p.contract
	// installedVersion := p.components.Version()
	// status.InstalledVersion = &installedVersion
	// p.provider.SetStatus(status)

	log.V(1).Info("Provider successfully installed", "name", p.provider.Name)
	// conditions.Set(p.provider, conditions.TrueCondition(operatorv1.ProviderInstalledCondition))
	return reconcile.Result{}, nil
}

// delete deletes the provider components using clusterctl library.
func (p *phaseReconciler) delete(ctx context.Context) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Deleting provider", "name", p.provider.Name)

	clusterClient := p.newClusterClient()

	// p.clusterctlProvider.Name = clusterctlProviderName(p.provider).Name
	// p.clusterctlProvider.Namespace = p.provider.GetNamespace()
	// p.clusterctlProvider.Type = string(util.ClusterctlProviderType(p.provider))
	// p.clusterctlProvider.ProviderName = p.provider.Name
	// if p.provider.GetStatus().InstalledVersion != nil {
	// 	p.clusterctlProvider.Version = *p.provider.GetStatus().InstalledVersion
	// } else {
	// 	p.clusterctlProvider.Version = p.options.Version
	// }

	err := clusterClient.ProviderComponents().Delete(cluster.DeleteOptions{
		Provider:         *p.clusterctlProvider,
		IncludeNamespace: false,
		IncludeCRDs:      false,
	})
	return reconcile.Result{}, wrapPhaseError(err, operatorv1.OldComponentsDeletionErrorReason, operatorv1.ProviderInstalledCondition)
}

// func clusterctlProviderName(provider provider) client.ObjectKey {
// 	prefix := ""
// 	switch provider.GetObject().(type) {
// 	case *operatorv1.BootstrapProvider:
// 		prefix = "bootstrap-"
// 	case *operatorv1.ControlPlaneProvider:
// 		prefix = "control-plane-"
// 	case *operatorv1.InfrastructureProvider:
// 		prefix = "infrastructure-"
// 	}

// 	return client.ObjectKey{Name: prefix + provider.Name, Namespace: provider.GetNamespace()}
// }

// newClusterClient returns a clusterctl client for interacting with management cluster.
func (s *phaseReconciler) newClusterClient() cluster.Client {
	return cluster.New(cluster.Kubeconfig{}, s.configClient, cluster.InjectProxy(&controllerProxy{
		ctrlClient: s.ctrlClient,
		ctrlConfig: s.ctrlConfig,
	}))
}

// preflightChecks performs preflight checks before installing provider.
func preflightChecks(ctx context.Context, c client.Client, provider provider) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	log.V(1).Info("Performing preflight checks", "provider", provider.Name)
	// TODO

	return ctrl.Result{}, nil
}
func clusterctlProviderType(provider provider) clusterctlv1.ProviderType {
	switch provider.Name {
	case "aws", "azure", "ibmcloud", "powervs", "vsphere", "openstack":
		return clusterctlv1.InfrastructureProviderType
	case "cluster-api":
		return clusterctlv1.CoreProviderType
	default:
		return clusterctlv1.ProviderTypeUnknown
	}
}
