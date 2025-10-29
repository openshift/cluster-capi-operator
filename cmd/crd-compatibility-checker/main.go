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
	"flag"
	"os"

	"github.com/spf13/pflag"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	klog "k8s.io/klog/v2"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/crdvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	capiflags "sigs.k8s.io/cluster-api/util/flags"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

func initScheme(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1alpha1.AddToScheme(scheme))
}

//nolint:funlen
func main() {
	scheme := runtime.NewScheme()
	initScheme(scheme)

	leaderElectionConfig := config.LeaderElectionConfiguration{
		LeaderElect:       true,
		LeaseDuration:     util.LeaseDuration,
		RenewDeadline:     util.RenewDeadline,
		RetryPeriod:       util.RetryPeriod,
		ResourceName:      "crd-compatibility-checker-leader",
		ResourceNamespace: "openshift-cluster-api",
	}

	healthAddr := flag.String(
		"health-addr",
		":9441",
		"The address for health checking.",
	)

	webhookPort := flag.Int(
		"webhook-port",
		9443,
		"The port for the webhook server to listen on.",
	)
	webhookCertDir := flag.String(
		"webhook-cert-dir",
		"/tmp/k8s-webhook-server/serving-certs/",
		"Webhook cert dir, only used when webhook-port is specified.",
	)

	logToStderr := flag.Bool(
		"logtostderr",
		true,
		"log to standard error instead of files",
	)

	// klog.Background will automatically use the right logger.
	ctrl.SetLogger(klog.Background())

	// Once all the flags are registered, switch to pflag
	// to allow leader election flags to be bound
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&leaderElectionConfig, pflag.CommandLine)

	// Register CAPI flags for the diagnostics endpoint.
	capiManagerOptions := capiflags.ManagerOptions{}
	capiflags.AddManagerOptions(pflag.CommandLine, &capiManagerOptions)

	pflag.Parse()

	if logToStderr != nil {
		klog.LogToStderr(*logToStderr)
	}

	cfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		HealthProbeBindAddress:  *healthAddr,
		LeaderElectionNamespace: leaderElectionConfig.ResourceNamespace,
		LeaderElection:          leaderElectionConfig.LeaderElect,
		LeaseDuration:           &leaderElectionConfig.LeaseDuration.Duration,
		LeaderElectionID:        leaderElectionConfig.ResourceName,
		RetryPeriod:             &leaderElectionConfig.RetryPeriod.Duration,
		RenewDeadline:           &leaderElectionConfig.RenewDeadline.Duration,
		WebhookServer: crwebhook.NewServer(crwebhook.Options{
			Port:    *webhookPort,
			CertDir: *webhookCertDir,
		}),
	})
	if err != nil {
		klog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	compatibilityRequirementReconciler := crdcompatibility.NewCompatibilityRequirementReconciler(mgr.GetClient())
	// Setup the CRD compatibility controller
	if err := compatibilityRequirementReconciler.SetupWithManager(ctx, mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "CompatibilityRequirement")
		os.Exit(1)
	}

	// Setup the validator for CustomResourceDefinition Create/Update/Delete events.
	crdValidator := crdvalidation.NewValidator(mgr.GetClient())
	if err := crdValidator.SetupWithManager(ctx, mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "CRDValidator")
		os.Exit(1)
	}

	objectValidator := objectvalidation.NewValidator()
	// Setup the objectvalidation controller and webhook
	if err := objectValidator.SetupWithManager(ctx, mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "ObjectValidator")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	klog.Info("Starting CRD compatibility checker manager")

	if err := mgr.Start(ctx); err != nil {
		klog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
