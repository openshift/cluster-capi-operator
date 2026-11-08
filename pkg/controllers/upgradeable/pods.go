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
	"fmt"
	"path/filepath"

	configv1 "github.com/openshift/api/config/v1"
	assessment "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errAssessmentPodFailed = errors.New("assessment pod failed")

// assessmentPodResources follows the resource-request convention of the operator's own
// deployments: requests without limits, which keeps the pods Burstable.
var assessmentPodResources = corev1.ResourceRequirements{
	Requests: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("10m"),
		corev1.ResourceMemory: resource.MustParse("50Mi"),
	},
}

func (r *UpgradeableController) basePod(name, command, currentVersion, targetVersion string, co *configv1.ClusterOperator) *corev1.Pod {
	owner := metav1.NewControllerRef(co, configv1.GroupVersion.WithKind("ClusterOperator"))
	owner.BlockOwnerDeletion = nil // Assessment cleanup must not block deletion of the operator.
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
			ServiceAccountName: name,
			// Match the operator's own deployments so the assessment pods are treated as
			// platform components rather than user workloads.
			PriorityClassName: "system-cluster-critical",
			// The process retries transient errors itself. Terminal exits must remain
			// visible to the controller rather than being hidden by container restarts.
			RestartPolicy: corev1.RestartPolicyNever,
			// A persistent API outage must eventually become a failed assessment,
			// not leave the operator reporting Progressing indefinitely.
			ActiveDeadlineSeconds: ptr.To(int64(1800)),
			Containers: []corev1.Container{{
				// Execute only code shipped in the current operator image; the target
				// release and provider images are mounted as data, never executed.
				Name: name, Image: r.ContainerImage,
				Command: []string{"/" + command}, Args: []string{currentVersion, targetVersion},
				Resources:                assessmentPodResources,
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}},
		},
	}
}

func (r *UpgradeableController) extractorPod(currentVersion string, target *configv1.Release, co *configv1.ClusterOperator) *corev1.Pod {
	// An empty target has no payload to mount. The extractor then records that
	// the assessment has no provider images rather than reading a release image.
	if target == nil {
		return r.basePod(assessment.ExtractorPodName, "upgradeable-payload-images-extractor", currentVersion, "", co)
	}

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
	// Let the node runtime pull images using its credentials, mirrors and proxy
	// configuration; the assessment process only reads the mounted contents.
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
		// Pod names are fixed, so recreation must wait for deletion to finish.
		return &result
	}

	if !labels.SelectorFromSet(desired.Labels).Matches(labels.Set(existing.Labels)) {
		// Pod spec and labels are fixed for an assessment, so labels alone identify
		// the assessment this pod was created for. Most PodSpec fields, including the
		// operator image, are immutable, and the versions they assess are unchanged.
		if err := r.deleteObject(ctx, existing); err != nil {
			return fail(err)
		}

		return &result
	}

	switch existing.Status.Phase {
	case corev1.PodSucceeded:
		return nil
	case corev1.PodFailed:
		// Start from observable inputs again, but return an error so controller
		// backoff paces repeated terminal failures instead of immediately spinning.
		if err := r.deleteObject(ctx, existing); err != nil {
			return fail(err)
		}

		return fail(fmt.Errorf("%w: %s (%s): %s; inspect its logs with oc logs -n %s %s", errAssessmentPodFailed, existing.Name, existing.Status.Reason, existing.Status.Message, existing.Namespace, existing.Name))
	default:
		return &result
	}
}
