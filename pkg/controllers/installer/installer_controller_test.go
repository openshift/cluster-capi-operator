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

package installer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

func getConfigMap(ctx context.Context, name string) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	cm.SetName(name)
	cm.SetNamespace("default")

	if err := cl.Get(ctx, client.ObjectKey{Name: name, Namespace: "default"}, cm); err != nil {
		return nil, err
	}

	return cm, nil
}

func checkConfigMap(ctx context.Context, name string) error {
	_, err := getConfigMap(ctx, name)
	return err
}

var _ = Describe("InstallerController", Serial, func() {
	BeforeEach(func(ctx context.Context) {
		createFixtures(ctx)
	}, defaultNodeTimeout)

	AfterEach(func(ctx context.Context) {
		// Ensure all managed objects are deleted between tests by reconciling
		// an empty revision.
		emptyRevision := addEmptyRevision(ctx)
		waitForRevision(ctx, emptyRevision.Name)
	}, defaultNodeTimeout)

	// Part 1: Core Installation Lifecycle

	Context("Core Installation Lifecycle", func() {
		It("installs objects from a single revision", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore)

			cm, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data).To(HaveKeyWithValue("version", "v1"))
		}, defaultNodeTimeout)

		It("installs a component with both CRDs and non-CRD objects", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerMixed)

			cm, err := getConfigMap(ctx, mixedCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data).To(HaveKeyWithValue("source", "mixed"))
		}, defaultNodeTimeout)

		It("updates objects when a new revision has updated content", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore)
			addRevisionAndWaitForSuccess(ctx, providerCoreV2)

			cm, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data).To(HaveKeyWithValue("version", "v2"))
		}, defaultNodeTimeout)

		It("creates new objects when a new revision adds components", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore)
			addRevisionAndWaitForSuccess(ctx, providerCore, providerInfra)

			cmA, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cmA.Data).To(HaveKeyWithValue("version", "v1"))

			cmB, err := getConfigMap(ctx, infraCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cmB.Data).To(HaveKeyWithValue("version", "v1"))
		}, defaultNodeTimeout)

		It("deletes objects when a new revision removes components", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore, providerInfra)

			// Verify both exist initially.
			Expect(checkConfigMap(ctx, coreCMName)).To(Succeed())
			Expect(checkConfigMap(ctx, infraCMName)).To(Succeed())

			addRevisionAndWaitForSuccess(ctx, providerCore)

			// Core ConfigMap should still exist.
			Expect(checkConfigMap(ctx, coreCMName)).To(Succeed())

			// Infra ConfigMap should be deleted via teardown of revision 1.
			Expect(checkConfigMap(ctx, infraCMName)).To(test.BeK8SNotFound())
		}, defaultNodeTimeout)

		It("tears down all objects when an empty revision is added", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore, providerInfra)

			// Verify objects exist.
			Expect(checkConfigMap(ctx, coreCMName)).To(Succeed())
			Expect(checkConfigMap(ctx, infraCMName)).To(Succeed())

			// Add an empty revision.
			emptyRevision := addEmptyRevision(ctx)
			waitForRevision(ctx, emptyRevision.Name)

			// Both should be deleted.
			Expect(checkConfigMap(ctx, coreCMName)).To(test.BeK8SNotFound())
			Expect(checkConfigMap(ctx, infraCMName)).To(test.BeK8SNotFound())
		}, defaultNodeTimeout)
	})

	Context("Object Management", func() {
		It("restores a modified managed object", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore)

			// Modify the managed ConfigMap.
			cm, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())

			Eventually(kWithCtx(ctx).Update(cm, func() {
				cm.Data["version"] = "modified"
			})).
				WithContext(ctx).
				WithTimeout(defaultEventuallyTimeout).
				Should(Succeed())

			// The controller should restore the original value.
			restored := &corev1.ConfigMap{}
			restored.SetName(coreCMName)
			restored.SetNamespace("default")

			Eventually(kWithCtx(ctx).Object(restored)).
				WithTimeout(defaultEventuallyTimeout).
				WithContext(ctx).
				Should(HaveField("Data", HaveKeyWithValue("version", "v1")))
		}, defaultNodeTimeout)

		It("re-creates a deleted managed object", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore)

			// Verify the ConfigMap exists.
			cm, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())

			// Delete the ConfigMap and wait for it to be not found
			Expect(cl.Delete(ctx, cm)).To(Succeed())

			// The controller should re-create it.
			// We will know it has been recreated because it will exist with a
			// new UID
			recreated := &corev1.ConfigMap{}
			recreated.SetName(coreCMName)
			recreated.SetNamespace("default")

			Eventually(kWithCtx(ctx).Object(recreated)).
				WithTimeout(defaultEventuallyTimeout).
				WithContext(ctx).
				Should(SatisfyAll(
					HaveField("UID", Not(Equal(cm.UID))),
					HaveField("Data", HaveKeyWithValue("version", "v1")),
				))
		}, defaultNodeTimeout)
	})

	Context("Waiting States", func() {
		It("reports WaitingOnExternal when ClusterAPI has no revisions", func(ctx context.Context) {
			// ClusterAPI exists but has no revisions (created by createFixtures).
			// Trigger reconcile to ensure the controller processes it.
			triggerReconcile()

			waitForConditions(ctx,
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonWaitingOnExternal),
			)
		}, defaultNodeTimeout)
	})

	Context("Error Handling", func() {
		It("reports NonRetryableError when a provider profile is missing", func(ctx context.Context) {
			// Write a revision referencing a provider not in our pool.
			clusterAPI := &operatorv1alpha1.ClusterAPI{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())

			clusterAPI.Status.Revisions = []operatorv1alpha1.ClusterAPIInstallerRevision{
				{
					Name:      "bogus-revision-1",
					Revision:  1,
					ContentID: "bogus-content-id",
					Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
						{
							Type: operatorv1alpha1.InstallerComponentTypeImage,
							Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
								Ref:     "registry.example.com/nonexistent@sha256:0000000000000000000000000000000000000000000000000000000000000000",
								Profile: "default",
							},
						},
					},
				},
			}
			clusterAPI.Status.DesiredRevision = "bogus-revision-1"
			Expect(cl.Status().Update(ctx, clusterAPI)).To(Succeed())

			waitForConditions(ctx,
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError),
				test.HaveCondition(conditionTypeDegraded).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonNonRetryableError),
			)
		}, defaultNodeTimeout)

		It("reports an error when duplicate objects exist across phases", func(ctx context.Context) {
			// providerCore and providerDupObj both define coreCMName.
			// Including both in a revision should produce a validation error
			// (duplicate object in different phases).
			addRevision(ctx, providerCore, providerDupObj)

			waitForConditions(ctx, SatisfyAll(
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("duplicate object found in phases")),
				test.HaveCondition(conditionTypeDegraded).
					WithStatus(configv1.ConditionTrue),
			))
		}, defaultNodeTimeout)

		It("adopts a pre-existing object when the adopt-existing annotation is set to always", func(ctx context.Context) {
			// Pre-create the ConfigMap that providerAdoptExisting will try to manage.
			cm := &corev1.ConfigMap{}
			cm.SetName(adoptCMName)
			cm.SetNamespace("default")
			cm.Data = map[string]string{"pre-existing": "true"}
			Expect(cl.Create(ctx, cm)).To(Succeed())

			DeferCleanup(func(ctx context.Context) {
				deleteAndWait(ctx, cm)
			})

			// With adopt-existing: always, the controller should adopt the
			// pre-existing object instead of reporting a collision.
			addRevisionAndWaitForSuccess(ctx, providerAdoptExisting)

			// Verify the object was adopted and updated to match the manifest.
			// Note: SSA only manages fields it declares; pre-existing fields
			// owned by other field managers are not removed.
			adopted, err := getConfigMap(ctx, adoptCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(adopted.Data).To(HaveKeyWithValue("version", "v1"))

			// Verify the adopt-existing annotation was stripped from the cluster object.
			Expect(adopted.GetAnnotations()).NotTo(HaveKey(revisiongenerator.AdoptExistingAnnotation))
		}, defaultNodeTimeout)

		It("reports NonRetryableError when adopt-existing annotation has an invalid value", func(ctx context.Context) {
			// The revision generator won't allow us to create a revision with
			// an invalid adopt-existing annotation, so we have to create it
			// manually. This ensures that the controller can recover from
			// shenanigans.
			profile := providersByName[providerAdoptInvalid]
			clusterAPI := &operatorv1alpha1.ClusterAPI{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())
			apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
				Name:      "invalid-annotation-1",
				Revision:  int64(len(clusterAPI.Status.Revisions) + 1),
				ContentID: "placeholder",
				Components: []operatorv1alpha1.ClusterAPIInstallerComponent{{
					Type: operatorv1alpha1.InstallerComponentTypeImage,
					Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
						Ref:     operatorv1alpha1.ImageDigestFormat(profile.ImageRef),
						Profile: profile.Profile,
					},
				}},
			}
			clusterAPI.Status.Revisions = append(clusterAPI.Status.Revisions, apiRev)
			clusterAPI.Status.DesiredRevision = apiRev.Name
			Expect(cl.Status().Update(ctx, clusterAPI)).To(Succeed())

			waitForConditions(ctx,
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError),
				test.HaveCondition(conditionTypeDegraded).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("invalid")),
			)
		}, defaultNodeTimeout)

		It("reports NonRetryableError when a collision occurs with an existing object", func(ctx context.Context) {
			// Pre-create the ConfigMap that providerCore will try to manage.
			// With the default CollisionProtectionPrevent, any pre-existing
			// object not already owned by boxcutter triggers a Collision.
			cm := &corev1.ConfigMap{}
			cm.SetName(coreCMName)
			cm.SetNamespace("default")
			cm.Data = map[string]string{"pre-existing": "true"}
			Expect(cl.Create(ctx, cm)).To(Succeed())

			DeferCleanup(func(ctx context.Context) {
				deleteAndWait(ctx, cm)
			})

			addRevision(ctx, providerCore)

			waitForConditions(ctx,
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError),
				test.HaveCondition(conditionTypeDegraded).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("collision")),
			)
		}, defaultNodeTimeout)

		It("continues reconciling a valid older revision when a newer revision is invalid", func(ctx context.Context) {
			// Install a valid revision first.
			addRevisionAndWaitForSuccess(ctx, providerCore)

			// Add a second revision referencing a non-existent provider.
			// reconcileRevision wraps the error as TerminalError; the older
			// valid revision is still reconciled via the tail handler.
			clusterAPI := &operatorv1alpha1.ClusterAPI{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())

			clusterAPI.Status.Revisions = append(clusterAPI.Status.Revisions,
				operatorv1alpha1.ClusterAPIInstallerRevision{
					Name:      "invalid-revision-2",
					Revision:  int64(len(clusterAPI.Status.Revisions) + 1),
					ContentID: "invalid-content-id",
					Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
						{
							Type: operatorv1alpha1.InstallerComponentTypeImage,
							Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
								Ref:     "registry.example.com/nonexistent@sha256:0000000000000000000000000000000000000000000000000000000000000000",
								Profile: "default",
							},
						},
					},
				})
			clusterAPI.Status.DesiredRevision = "invalid-revision-2"
			Expect(cl.Status().Update(ctx, clusterAPI)).To(Succeed())

			// The head revision is invalid so isComplete=false with a terminal error.
			// The controller reports NonRetryableError: Progressing=False, Degraded=True.
			waitForConditions(ctx,
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError),
				test.HaveCondition(conditionTypeDegraded).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonNonRetryableError),
			)

			// Despite the degraded state, the valid revision's objects should exist.
			cm, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data).To(HaveKeyWithValue("version", "v1"))

			// Modify the managed ConfigMap to verify drift correction still works.
			// The tracking cache watches are still active, so modifying a managed
			// object triggers re-reconciliation.
			Eventually(kWithCtx(ctx).Update(cm, func() {
				cm.Data["version"] = "modified"
			})).
				WithContext(ctx).
				WithTimeout(defaultEventuallyTimeout).
				Should(Succeed())

			// The controller should restore the original value.
			restored := &corev1.ConfigMap{}
			restored.SetName(coreCMName)
			restored.SetNamespace("default")

			Eventually(kWithCtx(ctx).Object(restored)).
				WithTimeout(defaultEventuallyTimeout).
				WithContext(ctx).
				Should(HaveField("Data", HaveKeyWithValue("version", "v1")))
		}, defaultNodeTimeout)
	})

	Context("Deployment Probes", func() {
		It("waits for Deployment to become Available before proceeding", func(ctx context.Context) {
			var revision operatorv1alpha1.ClusterAPIInstallerRevision

			// providerDeployment has InstallOrder=1, providerCore has InstallOrder=10,
			// so the Deployment phase comes first. The controller should wait for
			// the Deployment to become Available before creating the ConfigMap.
			By("adding a revision with a Deployment and a ConfigMap in separate phases", func() {
				revision = addRevision(ctx, providerDeployment, providerCore)

				// The Deployment should be created but the controller should report
				// Progressing=True while the Deployment is not yet Available.
				waitForConditions(ctx,
					test.HaveCondition(conditionTypeProgressing).
						WithStatus(configv1.ConditionTrue).
						WithReason(operatorstatus.ReasonProgressing).
						WithMessage(ContainSubstring("waiting on phase "+providerDeployment)),
				)
			})

			// The Deployment exists but the later-phase ConfigMap should NOT exist yet.
			deploy := &appsv1.Deployment{}
			deploy.SetName(deploymentName)
			deploy.SetNamespace("default")
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())
			Expect(checkConfigMap(ctx, coreCMName)).To(test.BeK8SNotFound())

			// Simulate the Deployment becoming Available by updating its status.
			By("simulating the Deployment becoming Available", func() {
				Eventually(kWithCtx(ctx).UpdateStatus(deploy, func() {
					deploy.Status.Conditions = []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
							Reason: "MinimumReplicasAvailable",
						},
					}
				})).
					WithContext(ctx).
					WithTimeout(defaultEventuallyTimeout).
					Should(Succeed())
			})

			// Now the controller should complete: later-phase objects created,
			// revision marked as current, Progressing=False.
			waitForRevision(ctx, revision.Name)

			cm, err := getConfigMap(ctx, coreCMName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cm.Data).To(HaveKeyWithValue("version", "v1"))
		}, defaultNodeTimeout)
	})

	Context("RelatedObjects", func() {
		It("does not produce relatedObjects for namespaced-only objects", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore)
			waitForRelatedObjects(ctx, Equal(staticRelatedObjects()))
		}, defaultNodeTimeout)

		It("produces relatedObjects for non-namespaced objects", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerClusterScoped)
			waitForRelatedObjects(ctx, ContainElement(configv1.ObjectReference{
				Group:    "rbac.authorization.k8s.io",
				Resource: "clusterroles",
				Name:     clusterRoleName,
			}))
		}, defaultNodeTimeout)

		It("produces relatedObjects for namespace objects", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerNamespace)
			waitForRelatedObjects(ctx, ContainElement(configv1.ObjectReference{
				Group:    "",
				Resource: "namespaces",
				Name:     testNamespaceName,
			}))
		}, defaultNodeTimeout)

		It("produces relatedObjects entry for CRD instances", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCRD)

			waitForRelatedObjects(ctx,
				ContainElement(configv1.ObjectReference{
					Group:    testCRDGVK.Group,
					Resource: "testwidgets",
				}),
			)
		}, defaultNodeTimeout)

		It("updates relatedObjects when components change", func(ctx context.Context) {
			numStatic := len(staticRelatedObjects())

			addRevisionAndWaitForSuccess(ctx, providerClusterScoped)
			waitForRelatedObjects(ctx, HaveLen(numStatic+1))

			addRevisionAndWaitForSuccess(ctx, providerClusterScoped, providerClusterScoped2)

			waitForRelatedObjects(ctx,
				HaveLen(numStatic+2),
				ContainElement(configv1.ObjectReference{
					Group:    "rbac.authorization.k8s.io",
					Resource: "clusterroles",
					Name:     clusterRoleName,
				}),
				ContainElement(configv1.ObjectReference{
					Group:    "rbac.authorization.k8s.io",
					Resource: "clusterroles",
					Name:     clusterRole2Name,
				}),
			)
		}, defaultNodeTimeout)

		It("retains relatedObjects while teardown revisions remain", func(ctx context.Context) {
			By("adding a revision with a single ClusterRole", func() {
				addRevisionAndWaitForSuccess(ctx, providerClusterScoped)
				waitForRelatedObjects(ctx, Not(BeEmpty()))
			})

			initial := getRelatedObjects(ctx)

			// Add an empty revision. The old revision remains in the
			// revision list being torn down, so its objects should still
			// appear in relatedObjects for must-gather.
			By("adding an empty revision", func() {
				emptyRevision := addEmptyRevision(ctx)
				waitForRevision(ctx, emptyRevision.Name)
			})

			Expect(getRelatedObjects(ctx)).To(Equal(initial))

			// Simulate the revision controller trimming old revisions
			// after currentRevision == desiredRevision. Only the latest
			// (empty) revision remains.
			By("trimming old revisions", func() {
				clusterAPI := &operatorv1alpha1.ClusterAPI{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI)).To(Succeed())

				clusterAPI.Status.Revisions = nil
				clusterAPI.Status.DesiredRevision = ""
				clusterAPI.Status.CurrentRevision = ""
				Expect(cl.Status().Update(ctx, clusterAPI)).To(Succeed())
			})

			// After trimming, relatedObjects should contain only the
			// static entries because there are no remaining revisions.
			waitForRelatedObjects(ctx, Equal(staticRelatedObjects()))
		}, defaultNodeTimeout)

		It("only includes non-namespaced objects in relatedObjects when mixed with namespaced", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerCore, providerClusterScoped, providerNamespace)
			waitForRelatedObjects(ctx,
				HaveLen(len(staticRelatedObjects())+2),
				ContainElement(configv1.ObjectReference{
					Group:    "rbac.authorization.k8s.io",
					Resource: "clusterroles",
					Name:     clusterRoleName,
				}),
				// Verify Namespace is collected with correct resource name
				ContainElement(configv1.ObjectReference{
					Group:    "",
					Resource: "namespaces",
					Name:     testNamespaceName,
				}),
			)
		}, defaultNodeTimeout)

		It("maintains stable relatedObjects order across reconciles", func(ctx context.Context) {
			addRevisionAndWaitForSuccess(ctx, providerManyClusterScoped)
			waitForRelatedObjects(ctx, Not(BeEmpty()))

			initial := getRelatedObjects(ctx)
			Expect(initial).To(HaveLen(10 + len(staticRelatedObjects())))

			// Perturb a managed object multiple times to trigger re-reconciliation.
			// Each time, verify relatedObjects has not changed.
			// We modify the rules field, which is managed by boxcutter via SSA,
			// so the controller will restore it.
			for i := range 3 {
				cr := &rbacv1.ClusterRole{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "test-cr-1"}, cr)).To(Succeed())

				Eventually(kWithCtx(ctx).Update(cr, func() {
					cr.Rules = []rbacv1.PolicyRule{
						{Verbs: []string{fmt.Sprintf("perturbation-%d", i)}, APIGroups: []string{""}, Resources: []string{"pods"}},
					}
				})).
					WithContext(ctx).
					WithTimeout(defaultEventuallyTimeout).
					Should(Succeed())

				// Wait for the controller to restore the object.
				restored := &rbacv1.ClusterRole{}
				restored.SetName("test-cr-1")
				Eventually(kWithCtx(ctx).Object(restored)).
					WithTimeout(defaultEventuallyTimeout).
					WithContext(ctx).
					Should(HaveField("Rules", BeEmpty()))

				Expect(getRelatedObjects(ctx)).To(Equal(initial))
			}
		}, defaultNodeTimeout)

		It("resolves resource names correctly via RESTMapper", func(ctx context.Context) {
			numStatic := len(staticRelatedObjects())

			addRevisionAndWaitForSuccess(ctx, providerVAP)

			// Verify ValidatingAdmissionPolicy is collected with correct resource name.
			waitForRelatedObjects(ctx,
				HaveLen(numStatic+1),
				ContainElement(configv1.ObjectReference{
					Group:    "admissionregistration.k8s.io",
					Resource: "validatingadmissionpolicies",
					Name:     vapName,
				}),
			)
		}, defaultNodeTimeout)

		It("uses CRD spec resource name for custom resources, not naive concatenation", func(ctx context.Context) {
			numStatic := len(staticRelatedObjects())

			addRevisionAndWaitForSuccess(ctx, providerIrregularCRD)

			// Verify relatedObjects contains the CRD instance type with correct resource name
			// extracted from the CRD spec.names.plural field (not RESTMapper)
			waitForRelatedObjects(ctx,
				HaveLen(numStatic+1),
				ContainElement(configv1.ObjectReference{
					Group:    "security.example.com",
					Resource: "policies",
				}),
			)
		}, defaultNodeTimeout)
	})
})

var _ = Describe("InstallerController proxy", Serial, func() {
	BeforeEach(func(ctx context.Context) {
		createFixtures(ctx)
	}, defaultNodeTimeout)

	AfterEach(func(ctx context.Context) {
		emptyRevision := addEmptyRevision(ctx)
		waitForRevision(ctx, emptyRevision.Name)
	}, defaultNodeTimeout)

	It("injects proxy env vars into deployed Deployments", func(ctx context.Context) {
		// Configure proxy before adding revision.
		proxy := &configv1.Proxy{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, proxy)).To(Succeed())
		proxy.Status = configv1.ProxyStatus{
			HTTPProxy:  "http://proxy:3128",
			HTTPSProxy: "https://proxy:3129",
			NoProxy:    ".cluster.local",
		}
		Expect(cl.Status().Update(ctx, proxy)).To(Succeed())

		// The revision content ID must match what the controller computes,
		// which includes proxy injection into Deployment manifests.
		proxyEnvVars := util.ProxyEnvVars(proxy)
		renderOpts := []revisiongenerator.RevisionRenderOption{
			revisiongenerator.WithProxyConfig(proxyEnvVars),
		}

		revision := addRevisionWithOpts(ctx, renderOpts, providerDeployment)
		makeDeploymentAvailable(ctx, deploymentName, "default")
		waitForRevision(ctx, revision.Name)

		// Re-read the Deployment and verify proxy env vars.
		deploy := &appsv1.Deployment{}
		deploy.SetName(deploymentName)
		deploy.SetNamespace("default")
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())

		env := deploy.Spec.Template.Spec.Containers[0].Env
		Expect(env).To(ContainElement(SatisfyAll(
			HaveField("Name", "HTTP_PROXY"),
			HaveField("Value", "http://proxy:3128"),
		)))
		Expect(env).To(ContainElement(SatisfyAll(
			HaveField("Name", "HTTPS_PROXY"),
			HaveField("Value", "https://proxy:3129"),
		)))
		Expect(env).To(ContainElement(SatisfyAll(
			HaveField("Name", "NO_PROXY"),
			HaveField("Value", ".cluster.local"),
		)))
	}, defaultNodeTimeout)

	It("does not inject proxy env vars when proxy is empty", func(ctx context.Context) {
		// Proxy is created with empty status by createFixtures.
		revision := addRevision(ctx, providerDeployment)
		makeDeploymentAvailable(ctx, deploymentName, "default")
		waitForRevision(ctx, revision.Name)

		deploy := &appsv1.Deployment{}
		deploy.SetName(deploymentName)
		deploy.SetNamespace("default")
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())

		for _, ev := range deploy.Spec.Template.Spec.Containers[0].Env {
			Expect(ev.Name).NotTo(BeElementOf("HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"))
		}
	}, defaultNodeTimeout)
})

var _ = Describe("InstallerController without ClusterAPI", Serial, func() {
	It("reports WaitingOnExternal when ClusterAPI does not exist", func(ctx context.Context) {
		createFixturesWithoutClusterAPI(ctx)

		// Trigger reconciliation via the channel source since no ClusterAPI
		// object exists for the For() watch.
		triggerReconcile()

		waitForConditions(ctx,
			test.HaveCondition(conditionTypeProgressing).
				WithStatus(configv1.ConditionTrue).
				WithReason(operatorstatus.ReasonWaitingOnExternal),
		)
	}, defaultNodeTimeout)
})

func TestInstallerControllerError(t *testing.T) {
	c := &InstallerController{}

	t.Run("only terminal errors returns non-retryable error", func(t *testing.T) {
		g := NewWithT(t)

		termErr := reconcile.TerminalError(fmt.Errorf("provider not found"))
		result := c.error([]error{termErr})

		g.Expect(errors.Is(result.Error(), reconcile.TerminalError(nil))).To(BeTrue())
		g.Expect(result.Error()).To(MatchError(ContainSubstring("provider not found")))
	})

	t.Run("only non-terminal errors returns retryable error", func(t *testing.T) {
		g := NewWithT(t)

		transientErr := fmt.Errorf("connection refused")
		result := c.error([]error{transientErr})

		g.Expect(errors.Is(result.Error(), reconcile.TerminalError(nil))).To(BeFalse())
		g.Expect(result.Error()).To(MatchError(ContainSubstring("connection refused")))
	})

	t.Run("mixed terminal and non-terminal errors returns retryable error", func(t *testing.T) {
		g := NewWithT(t)

		termErr := reconcile.TerminalError(fmt.Errorf("provider not found"))
		transientErr := fmt.Errorf("connection refused")
		result := c.error([]error{termErr, transientErr})

		g.Expect(errors.Is(result.Error(), reconcile.TerminalError(nil))).To(BeFalse())
		g.Expect(result.Error()).To(MatchError(ContainSubstring("provider not found")))
		g.Expect(result.Error()).To(MatchError(ContainSubstring("connection refused")))
	})
}
