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
package extractimages

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const manifestPath = "/release-image/release-manifests/0000_30_cluster-api-operator_05_provider-images-configmap.yaml"

var errInvalidManifest = errors.New("payload manifest must be a v1 ConfigMap containing provider images")

// Run executes the payload extraction pod entrypoint.
func Run(ctx context.Context, args []string) error {
	return pods.Run(ctx, "upgradeable-payload-images-extractor", args, func(ctx context.Context, c client.Client, current, target string) error {
		return Step(ctx, c, current, target, manifestPath)
	})
}

// Step applies only the payload's provider image data to the assessment ConfigMap.
func Step(ctx context.Context, c client.Client, current, target, path string) error {
	// The mounted payload is immutable, so retries cannot repair a missing or
	// malformed manifest. Treat these input failures as terminal.
	data, err := os.ReadFile(path) //nolint:gosec // path is supplied by the operator's image volume mount
	if err != nil {
		return pods.Terminal(fmt.Errorf("reading payload manifest: %w", err))
	}

	source := &corev1.ConfigMap{}
	if err := yaml.UnmarshalStrict(data, source); err != nil {
		return pods.Terminal(fmt.Errorf("parsing payload manifest: %w", err))
	}

	if source.Kind != "ConfigMap" || source.APIVersion != "v1" || len(source.Data) == 0 {
		return pods.Terminal(errInvalidManifest)
	}

	owner, err := pods.OwnerReference(ctx, c)
	if err != nil {
		return err
	}

	// Payload metadata belongs to the target release, not to this assessment.
	// Copy only image references so annotations cannot alter assessment behavior.
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name: pods.ImagesConfigMapName, Namespace: pods.Namespace,
			Labels: pods.AssessmentLabels(current, target), OwnerReferences: []metav1.OwnerReference{owner},
		}, Data: source.Data,
	}
	// The ConfigMap is applied in place, never recreated, so both its data and its
	// assessment labels are owned by this field manager. SSA replays writes safely
	// after an ambiguous API failure and repairs drift in those fields, including
	// an earlier assessment's labels, which are what the controller reads next.
	if err := c.Patch(ctx, cm, client.Apply, operatorstatus.CAPIFieldOwner("UpgradeableExtractor"), client.ForceOwnership); err != nil {
		return pods.APIError(fmt.Errorf("applying payload images ConfigMap: %w", err))
	}

	return nil
}
