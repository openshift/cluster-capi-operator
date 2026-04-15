// Copyright 2025 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility"
	crdcompatibilitybindata "github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/bindata"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/crdvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectpruning"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/staticresourceinstaller"

	ctrl "sigs.k8s.io/controller-runtime"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
)

func initScheme(scheme *runtime.Scheme) {
	utilruntime.Must(admissionregistrationv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(operatorv1.AddToScheme(scheme))
}

const (
	managerName             = "crd-compatibility-checker"
	defaultManagerNamespace = "openshift-compatibility-requirements-operator"
)

func main() {
	cfg := ctrl.GetConfigOrDie()
	ctx := ctrl.SetupSignalHandler()
	scheme := runtime.NewScheme()
	initScheme(scheme)

	extraflags := flag.NewFlagSet("", flag.ContinueOnError)
	webhookPort := extraflags.Int(
		"webhook-port",
		9443,
		"The port for the webhook server to listen on.",
	)
	webhookCertDir := extraflags.String(
		"webhook-cert-dir",
		"/tmp/k8s-webhook-server/serving-certs/",
		"Webhook cert dir, only used when webhook-port is specified.",
	)

	log, operatorConfig, mgrOpts, initManager, err := commoncmdoptions.InitOperatorConfig(ctx, cfg, scheme, managerName, defaultManagerNamespace, extraflags)
	if err != nil {
		log.Error(err, "unable to initialize operator config")
		os.Exit(1)
	}

	mgrOpts.WebhookServer = crwebhook.NewServer(crwebhook.Options{
		Port:    *webhookPort,
		CertDir: *webhookCertDir,
		TLSOpts: operatorConfig.TLSOptions,
	})

	ctx, cancel := context.WithCancel(ctx)

	mgr, err := initManager(ctx, cancel, mgrOpts)
	if err != nil {
		log.Error(err, "unable to initialize manager")
		os.Exit(1)
	}

	if err := setupControllers(ctx, mgr); err != nil {
		log.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	log.Info("Starting CRD compatibility checker manager")

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupControllers(ctx context.Context, mgr ctrl.Manager) error {
	// Setup the CRD compatibility controller
	compatibilityRequirementReconciler := crdcompatibility.NewCompatibilityRequirementReconciler(mgr.GetClient())
	if err := compatibilityRequirementReconciler.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller %s: %w", "CompatibilityRequirement", err)
	}

	// Setup the validator for CustomResourceDefinition Create/Update/Delete events.
	crdValidator := crdvalidation.NewValidator(mgr.GetClient())
	if err := crdValidator.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller %s: %w", "CRDValidator", err)
	}

	staticResourceInstaller := staticresourceinstaller.NewStaticResourceInstallerController(crdcompatibilitybindata.Assets)
	if err := staticResourceInstaller.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller %s: %w", "StaticResourceInstaller", err)
	}

	// Setup the objectvalidation webhook
	objectValidator := objectvalidation.NewValidator()
	if err := objectValidator.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller %s: %w", "ObjectValidator", err)
	}

	// Setup the objectpruning controller and webhook
	objectPruner := objectpruning.NewValidator()
	if err := objectPruner.SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller %s: %w", "ObjectPruner", err)
	}

	return nil
}
