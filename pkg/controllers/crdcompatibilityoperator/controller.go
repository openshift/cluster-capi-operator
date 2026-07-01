// Copyright 2026 Red Hat, Inc.
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
package crdcompatibilityoperator

import (
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	controllerName            = "CRDCompatibilityOperatorController"
	operandDeploymentName     = "compatibility-requirements-controllers"
	operandServiceAccountName = "compatibility-requirements-controllers"
	operandPDBName            = "compatibility-requirements-controllers-pdb"
	operandLabel              = "compatibility-requirements-controllers"
	webhookCertSecretName     = "compatibility-requirements-controllers-webhook-service-cert"
	metricsCertSecretName     = "compatibility-requirements-controllers-metrics-tls"
	releaseVersionEnv         = "RELEASE_VERSION"
	defaultReleaseVersion     = "0.0.1-snapshot"
)

// CRDCompatibilityOperatorController manages the CRD Compatibility Checker operand based on cluster topology.
type CRDCompatibilityOperatorController struct {
	client       client.Client
	scheme       *runtime.Scheme
	namespace    string
	operandImage string
}

// NewCRDCompatibilityOperatorController creates a new controller instance.
func NewCRDCompatibilityOperatorController(client client.Client, scheme *runtime.Scheme, namespace, operandImage string) *CRDCompatibilityOperatorController {
	return &CRDCompatibilityOperatorController{
		client:       client,
		scheme:       scheme,
		namespace:    namespace,
		operandImage: operandImage,
	}
}

// SetupWithManager registers the controller with the manager.
func (r *CRDCompatibilityOperatorController) SetupWithManager(mgr ctrl.Manager) error {
	// Single fixed reconcile key for all events
	toFixedKey := func(ctx context.Context, _ client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "fixed"}}}
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		// Watch Infrastructure CR (name == "cluster")
		For(&configv1.Infrastructure{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetName() == controllers.InfrastructureResourceName
		}))).
		// Watch operand Deployment
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(toFixedKey), builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == r.namespace && obj.GetName() == operandDeploymentName
		}))).
		// Watch operand PDB
		Watches(&policyv1.PodDisruptionBudget{}, handler.EnqueueRequestsFromMapFunc(toFixedKey), builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == r.namespace && obj.GetName() == operandPDBName
		}))).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// Reconcile manages the operand Deployment and PDB based on cluster topology.
func (r *CRDCompatibilityOperatorController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("Reconciling CRD Compatibility Checker operand")

	// Get Infrastructure CR
	infra, err := util.GetInfra(ctx, r.client)
	if err != nil {
		log.Error(err, "Failed to get Infrastructure resource")
		return ctrl.Result{}, fmt.Errorf("failed to get infrastructure: %w", err)
	}

	// Determine desired replica count based on topology
	topology := infra.Status.ControlPlaneTopology
	desiredReplicas := int32(2)
	createPDB := true

	if topology == configv1.SingleReplicaTopologyMode {
		desiredReplicas = 1
		createPDB = false
	}

	log.Info("Determined topology configuration", "topology", topology, "replicas", desiredReplicas, "pdb", createPDB)

	// Reconcile Deployment
	if err := r.reconcileDeployment(ctx, desiredReplicas); err != nil {
		log.Error(err, "Failed to reconcile Deployment")
		return ctrl.Result{}, err
	}

	// Reconcile PDB
	if err := r.reconcilePDB(ctx, createPDB); err != nil {
		log.Error(err, "Failed to reconcile PodDisruptionBudget")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled CRD Compatibility Checker operand")

	return ctrl.Result{}, nil
}

//nolint:funlen
func (r *CRDCompatibilityOperatorController) reconcileDeployment(ctx context.Context, replicas int32) error {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operandDeploymentName,
			Namespace: r.namespace,
		},
	}

	_, err := controllerutil.CreateOrPatch(ctx, r.client, deployment, func() error {
		deployment.Spec.Replicas = &replicas
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"k8s-app": operandLabel,
			},
		}

		deployment.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"k8s-app": operandLabel,
				},
				Annotations: map[string]string{
					"target.workload.openshift.io/management": `{"effect": "PreferredDuringScheduling"}`,
					"openshift.io/required-scc":               "restricted-v3",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: operandServiceAccountName,
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
							{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"k8s-app": operandLabel,
										},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:    "compatibility-requirements-controllers",
						Image:   r.operandImage,
						Command: []string{"./crd-compatibility-checker"},
						Args:    []string{"--diagnostics-address=:8443"},
						Env: []corev1.EnvVar{
							{
								Name: releaseVersionEnv,
								Value: func() string {
									if v := os.Getenv(releaseVersionEnv); v != "" {
										return v
									}

									return defaultReleaseVersion
								}(),
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/readyz",
									Port: intstr.FromString("healthz"),
								},
							},
							PeriodSeconds:       10,
							TimeoutSeconds:      5,
							FailureThreshold:    3,
							SuccessThreshold:    1,
							InitialDelaySeconds: 0,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("healthz"),
								},
							},
							PeriodSeconds:       10,
							TimeoutSeconds:      5,
							FailureThreshold:    3,
							SuccessThreshold:    1,
							InitialDelaySeconds: 0,
						},
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 9443,
								Name:          "webhook-server",
								Protocol:      corev1.ProtocolTCP,
							},
							{
								ContainerPort: 9440,
								Name:          "healthz",
								Protocol:      corev1.ProtocolTCP,
							},
							{
								ContainerPort: 8443,
								Name:          "diagnostics",
								Protocol:      corev1.ProtocolTCP,
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("50Mi"),
							},
						},
						TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "cert",
								MountPath: "/tmp/k8s-webhook-server/serving-certs",
								ReadOnly:  true,
							},
							{
								Name:      "metrics-cert",
								MountPath: "/tmp/k8s-metrics-server/serving-certs",
								ReadOnly:  true,
							},
						},
					},
				},
				NodeSelector: map[string]string{
					"node-role.kubernetes.io/control-plane": "",
				},
				PriorityClassName: "system-cluster-critical",
				RestartPolicy:     corev1.RestartPolicyAlways,
				Tolerations: []corev1.Toleration{
					{
						Key:      "node-role.kubernetes.io/master",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
					{
						Key:      "node-role.kubernetes.io/control-plane",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  webhookCertSecretName,
								DefaultMode: ptr.To(int32(420)),
							},
						},
					},
					{
						Name: "metrics-cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  metricsCertSecretName,
								DefaultMode: ptr.To(int32(420)),
							},
						},
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or patch Deployment: %w", err)
	}

	return nil
}

// reconcilePDB creates or deletes the PodDisruptionBudget based on cluster topology.
// For HA topologies (create=true), it ensures a PDB exists with minAvailable=1.
// For SNO topology (create=false), it ensures the PDB does not exist.
func (r *CRDCompatibilityOperatorController) reconcilePDB(ctx context.Context, create bool) error {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operandPDBName,
			Namespace: r.namespace,
		},
	}

	if !create {
		// SNO: ensure PDB does not exist
		err := r.client.Delete(ctx, pdb)
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete PodDisruptionBudget: %w", err)
		}

		return nil
	}

	// HA: ensure PDB exists
	_, err := controllerutil.CreateOrPatch(ctx, r.client, pdb, func() error {
		minAvailable := intstr.FromInt32(1)
		pdb.Spec.MinAvailable = &minAvailable
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"k8s-app": operandLabel,
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or patch PodDisruptionBudget: %w", err)
	}

	return nil
}
