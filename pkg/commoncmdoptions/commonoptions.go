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
package commoncmdoptions

import (
	"crypto/tls"
	"errors"
	"flag"
	"os"
	"time"

	capiflags "sigs.k8s.io/cluster-api/util/flags"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
)

// The default durations for the leader election operations.
//
//nolint:gochecknoglobals
var (
	// LeaseDuration is the default duration for the leader election lease.
	LeaseDuration = metav1.Duration{Duration: 137 * time.Second}
	// RenewDeadline is the default duration for the leader renewal.
	RenewDeadline = metav1.Duration{Duration: 107 * time.Second}
	// RetryPeriod is the default duration for the leader election retrial.
	RetryPeriod = metav1.Duration{Duration: 26 * time.Second}
)

// CommonOptions contains options common to all managers.
type CommonOptions struct {
	HealthAddr           *string
	CAPINamespace        *string
	MAPINamespace        *string
	OperatorNamespace    *string
	LogToStderr          *bool
	CapiManagerOptions   capiflags.ManagerOptions
	TextLoggerConfig     *textlogger.Config
	LeaderElectionConfig config.LeaderElectionConfiguration

	managerName string
}

// InitCommonOptions configures command line flags for options common to all managers.
func InitCommonOptions(managerName, defaultManagerNamespace string) *CommonOptions {
	opts := &CommonOptions{
		HealthAddr:        flag.String("health-addr", ":9440", "The address for health checking."),
		CAPINamespace:     flag.String("capi-namespace", controllers.DefaultCAPINamespace, "The namespace where CAPI components are installed."),
		MAPINamespace:     flag.String("mapi-namespace", controllers.DefaultMAPINamespace, "The namespace where MAPI components are installed."),
		OperatorNamespace: flag.String("operator-namespace", controllers.DefaultOperatorNamespace, "The namespace where the operator will run."),
		LogToStderr:       flag.Bool("logtostderr", true, "log to standard error instead of files"),

		CapiManagerOptions: capiflags.ManagerOptions{},

		TextLoggerConfig: textlogger.NewConfig(),

		LeaderElectionConfig: config.LeaderElectionConfiguration{
			LeaderElect:       true,
			LeaseDuration:     LeaseDuration,
			RenewDeadline:     RenewDeadline,
			RetryPeriod:       RetryPeriod,
			ResourceName:      managerName + "-leader",
			ResourceNamespace: defaultManagerNamespace,
		},

		managerName: managerName,
	}

	capiflags.AddManagerOptions(pflag.CommandLine, &opts.CapiManagerOptions)
	opts.TextLoggerConfig.AddFlags(flag.CommandLine)

	return opts
}

// Parse parses the command line flags, binding values to the CommonOptions. It
// also initialises the global logger based on the given options.
func (opts *CommonOptions) Parse() {
	// Once all the flags are registered, switch to pflag
	// to allow leader election flags to be bound
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	options.BindLeaderElectionFlags(&opts.LeaderElectionConfig, pflag.CommandLine)

	pflag.Parse()

	if opts.LogToStderr != nil {
		klog.LogToStderr(*opts.LogToStderr)
	}

	ctrl.SetLogger(textlogger.NewLogger(opts.TextLoggerConfig))
}

// GetCommonManagerOptions returns a ctrl.Options struct which has been
// initialised with values from the command line.
func (opts *CommonOptions) GetCommonManagerOptions() (ctrl.Options, []func(config *tls.Config)) {
	tlsOptions, diagnosticsOpts, err := capiflags.GetManagerOptions(opts.CapiManagerOptions)
	if err != nil {
		klog.Error(err, "unable to get manager options")
		os.Exit(1)
	}

	return ctrl.Options{
		Metrics:                       *diagnosticsOpts,
		HealthProbeBindAddress:        *opts.HealthAddr,
		LeaderElectionNamespace:       opts.LeaderElectionConfig.ResourceNamespace,
		LeaderElection:                opts.LeaderElectionConfig.LeaderElect,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &opts.LeaderElectionConfig.LeaseDuration.Duration,
		LeaderElectionID:              opts.LeaderElectionConfig.ResourceName,
		RetryPeriod:                   &opts.LeaderElectionConfig.RetryPeriod.Duration,
		RenewDeadline:                 &opts.LeaderElectionConfig.RenewDeadline.Duration,
	}, tlsOptions
}

// GetClusterOperatorStatusClient returns a ClusterOperatorStatusClient struct which has been
// initialised with values from the command line.
func (opts *CommonOptions) GetClusterOperatorStatusClient(mgr ctrl.Manager, platform configv1.PlatformType, controllerName string) operatorstatus.ClusterOperatorStatusClient {
	return operatorstatus.ClusterOperatorStatusClient{
		Client:            mgr.GetClient(),
		Recorder:          mgr.GetEventRecorderFor(opts.managerName + "-" + controllerName),
		ReleaseVersion:    util.GetReleaseVersion(),
		ManagedNamespace:  *opts.CAPINamespace,
		OperatorNamespace: *opts.OperatorNamespace,
		Platform:          platform,
	}
}

// AddCommonChecks adds the common health and readyz checks to the manager.
func AddCommonChecks(mgr ctrl.Manager) error {
	return errors.Join(
		mgr.AddHealthzCheck("health", healthz.Ping),
		mgr.AddReadyzCheck("check", healthz.Ping),
	)
}
