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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	assessment "github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const podSpecHashAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/upgradeable-pod-spec-hash"

var errAssessmentPodFailed = errors.New("assessment pod failed")

func (r *UpgradeableController) basePod(name, command, currentVersion, targetVersion string, co *configv1.ClusterOperator) *corev1.Pod {
	owner := metav1.NewControllerRef(co, configv1.GroupVersion.WithKind("ClusterOperator"))
	owner.BlockOwnerDeletion = nil
	podLabels := assessment.AssessmentLabels(currentVersion, targetVersion)
	podLabels["k8s-app"] = name

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: assessment.Namespace,
			Labels:          podLabels,
			Annotations:     map[string]string{"openshift.io/required-scc": "restricted-v2"},
			OwnerReferences: []metav1.OwnerReference{*owner},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:    name,
			RestartPolicy:         corev1.RestartPolicyNever,
			ActiveDeadlineSeconds: ptr.To(int64(1800)),
			Containers: []corev1.Container{{
				Name: name, Image: r.ContainerImage,
				Command: []string{"/" + command}, Args: []string{currentVersion, targetVersion},
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}},
		},
	}
}

func (r *UpgradeableController) extractorPod(currentVersion string, target *configv1.Release, co *configv1.ClusterOperator) *corev1.Pod {
	pod := r.basePod(assessment.ExtractorPodName, "upgradeable-payload-images-extractor", currentVersion, target.Version, co)
	addImageVolume(pod, "release-image", target.Image, "/release-image")

	return pod
}

func (r *UpgradeableController) requirementsPod(currentVersion, targetVersion string, cm *corev1.ConfigMap, co *configv1.ClusterOperator) *corev1.Pod {
	pod := r.basePod(assessment.RequirementsPodName, "upgradeable-compatibility-requirements", currentVersion, targetVersion, co)

	images := sets.New[string]()
	for _, image := range cm.Data {
		images.Insert(image)
	}

	for _, image := range sets.List(images) {
		name := providerimages.VolumeNameForImageRef(image)
		addImageVolume(pod, name, image, filepath.Join(providerimages.ProviderImageMountBase, name))
	}

	return pod
}

func addImageVolume(pod *corev1.Pod, name, image, mountPath string) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{
		Image: &corev1.ImageVolumeSource{Reference: image, PullPolicy: corev1.PullIfNotPresent},
	}})
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: name, MountPath: mountPath, ReadOnly: true})
}

// A nil result means the pod completed its step successfully.
func (r *UpgradeableController) ensureAssessmentPod(ctx context.Context, desired *corev1.Pod) *UpgradeableResult {
	result := assessmentWaiting(desired.Name)
	fail := func(err error) *UpgradeableResult {
		result := assessmentError(err)
		return &result
	}
	// The hash detects changes to fields removed from the desired spec as well as
	// additions. DeepDerivative tolerates API defaults and admission injections.
	data, err := json.Marshal(desired.Spec)
	if err != nil {
		return fail(fmt.Errorf("serializing assessment pod: %w", err))
	}

	desired.Annotations[podSpecHashAnnotation] = fmt.Sprintf("%x", sha256.Sum256(data))

	existing := &corev1.Pod{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return fail(fmt.Errorf("getting assessment pod: %w", err))
		}

		if err := r.Create(ctx, desired); err != nil {
			return fail(fmt.Errorf("creating assessment pod: %w", err))
		}

		return &result
	}

	if existing.DeletionTimestamp != nil {
		return &result
	}

	if !labels.SelectorFromSet(desired.Labels).Matches(labels.Set(existing.Labels)) ||
		existing.Annotations[podSpecHashAnnotation] != desired.Annotations[podSpecHashAnnotation] ||
		!equality.Semantic.DeepDerivative(desired.Spec, existing.Spec) {
		if err := r.deleteObject(ctx, existing); err != nil {
			return fail(err)
		}

		return &result
	}

	switch existing.Status.Phase {
	case corev1.PodSucceeded:
		return nil
	case corev1.PodFailed:
		if err := r.deleteObject(ctx, existing); err != nil {
			return fail(err)
		}

		return fail(fmt.Errorf("%w: %s (%s): %s; inspect its logs with oc logs -n %s %s", errAssessmentPodFailed, existing.Name, existing.Status.Reason, existing.Status.Message, existing.Namespace, existing.Name))
	default:
		return &result
	}
}
