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
package upgradeable

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-logr/logr"
	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Assessment labels and fixed resource identities are shared by the controller and pods.
const (
	CurrentVersionLabel = operatorstatus.CAPIOperatorIdentifierDomain + "/upgradeable-current-version"
	TargetVersionLabel  = operatorstatus.CAPIOperatorIdentifierDomain + "/upgradeable-target-version"
	ManagedLabel        = operatorstatus.CAPIOperatorIdentifierDomain + "/upgradeable-managed"
	Namespace           = "openshift-cluster-api-operator"
	ImagesConfigMapName = "upgradeable-payload-images"
	ExtractorPodName    = "upgradeable-payload-images-extractor"
	RequirementsPodName = "upgradeable-compatibility-requirements"
	ManagedLabelValue   = "true"
)

// AssessmentLabels identifies all resources belonging to one assessment.
func AssessmentLabels(current, target string) map[string]string {
	return map[string]string{CurrentVersionLabel: current, TargetVersionLabel: target, ManagedLabel: ManagedLabelValue}
}

type terminalError struct{ err error }

var errInvalidArguments = errors.New("expected currentVersion and targetVersion arguments")

func (e terminalError) Error() string { return e.err.Error() }
func (e terminalError) Unwrap() error { return e.err }

// Terminal marks an error as requiring pod recreation rather than an in-process retry.
func Terminal(err error) error {
	if err == nil {
		return nil
	}

	return terminalError{err}
}

// IsTerminal checks whether the step encountered an unrecoverable input error.
func IsTerminal(err error) bool { var t terminalError; return errors.As(err, &t) }

// APIError marks invalid inputs and missing permissions as terminal failures.
func APIError(err error) error {
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		return Terminal(err)
	}

	return err
}

// OwnerReference resolves the cluster scoped owner shared by assessment resources.
func OwnerReference(ctx context.Context, c client.Client) (metav1.OwnerReference, error) {
	co := &configv1.ClusterOperator{}
	if err := c.Get(ctx, client.ObjectKey{Name: operatorstatus.ClusterOperatorName}, co); err != nil {
		return metav1.OwnerReference{}, APIError(fmt.Errorf("getting ClusterOperator owner: %w", err))
	}

	owner := metav1.NewControllerRef(co, configv1.GroupVersion.WithKind("ClusterOperator"))
	// Assessment cleanup does not block deletion of the ClusterOperator.
	owner.BlockOwnerDeletion = nil

	return *owner, nil
}

// Run executes a pod entrypoint with structured logging and API retries.
func Run(ctx context.Context, name string, args []string, step func(context.Context, client.Client, string, string) error) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)

	flags.Usage = func() {
		_, _ = fmt.Fprintf(flags.Output(), "Usage: %s currentVersion targetVersion\n", name)
	}
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parsing arguments: %w", err)
	}

	if flags.NArg() != 2 {
		return fmt.Errorf("%w; usage: %s currentVersion targetVersion", errInvalidArguments, name)
	}

	args = flags.Args()

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading API configuration: %w", err)
	}

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, configv1.AddToScheme, openshiftapiextensionsv1.AddToScheme} {
		if err := add(scheme); err != nil {
			return err
		}
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating API client: %w", err)
	}

	log := logr.FromSlogHandler(slog.NewJSONHandler(os.Stderr, nil)).WithName(name)
	log.Info("Starting assessment", "currentVersion", args[0], "targetVersion", args[1])

	err = Loop(ctx, log, func() error { return step(ctx, c, args[0], args[1]) })
	if err != nil {
		log.Error(err, "Assessment failed")
	} else {
		log.Info("Assessment completed")
	}

	return err
}

// Loop repeats transient failures with bounded exponential backoff until completion.
func Loop(ctx context.Context, log logr.Logger, step func() error) error {
	delay := time.Second

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("assessment canceled: %w", err)
		}

		err := step()
		if err == nil {
			return nil
		}

		if IsTerminal(err) {
			return err
		}

		log.Error(err, "step failed, retrying", "attempt", attempt, "backoff", delay)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("assessment canceled: %w", ctx.Err())
		case <-timer.C:
		}

		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}
