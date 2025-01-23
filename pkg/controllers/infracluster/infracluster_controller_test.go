/*
Copyright 2024 Red Hat, Inc.

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
package infracluster

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"

	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

const awsTestRegion = "us-east-1"

var _ = Describe("InfraCluster", func() {
	var mgrCtx context.Context
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var bareInfraCluster *awsv1.AWSCluster

	ocpInfraClusterName := "test-infra-cluster-name"
	ocpInfraAWS := configv1resourcebuilder.Infrastructure().AsAWS(ocpInfraClusterName, awsTestRegion).Build()

	infraClusterWithExternallyManagedByAnnotation := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ocpInfraClusterName,
			Namespace: defaultCAPINamespace,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
	}

	// When the annotation is present and the value is set to
	// managedByAnnotationValueClusterCAPIOperatorInfraClusterController, it is managed by this controller.
	// When the annotation is present with any other value, it is externally owned.
	thirdPartyAnnotation := "thirdparty"
	infraClusterWithExternallyManagedByAnnotationWithValue := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ocpInfraClusterName,
			Namespace: defaultCAPINamespace,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: thirdPartyAnnotation,
			},
		},
	}

	infraClusterWithoutExternallyManagedByAnnotation := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ocpInfraClusterName,
			Namespace: defaultCAPINamespace,
		},
	}

	BeforeEach(func() {
		bareInfraCluster = &awsv1.AWSCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ocpInfraClusterName,
				Namespace: defaultCAPINamespace,
			},
		}
		// Create ClusterOperator.
		Expect(cl.Create(ctx, configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())).To(Succeed())
		// Create CAPI Namespace.
		Expect(cl.Create(ctx, corev1resourcebuilder.Namespace().WithName(defaultCAPINamespace).Build())).To(Succeed())
		// Setup and Start Manager.
		mgrCtx, mgrCancel = context.WithCancel(context.Background())
		mgrDone = make(chan struct{})
		startManager(mgrCtx, mgrDone, ocpInfraAWS)
	})

	AfterEach(func() {
		// Stop Manager.
		stopManager(mgrCancel, mgrDone)
		// Cleanup Resources.
		testutils.CleanupResources(Default, ctx, cfg, cl, "", &configv1.ClusterOperator{})
		testutils.CleanupResources(Default, ctx, cfg, cl, defaultCAPINamespace, &awsv1.AWSCluster{})
	})

	Context("When there is no InfraCluster", func() {
		It("should create an InfraCluster, with Ready: true and externally ManagedBy Annotation", func() {
			Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
				HaveField("Status.Ready", BeTrue()),
				HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
			))
		})

		It("should update the ClusterOperator status conditions with controller specific ones to reflect a normal state", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())).Should(
				HaveField("Status.Conditions", SatisfyAll(
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerAvailableCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionTrue)),
					)),
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerDegradedCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionFalse)),
					)),
				)),
			)
		})
	})

	Context("When there is an InfraCluster with no externally ManagedBy Annotation", func() {
		Context("When the InfraCluster is Ready", func() {
			BeforeEach(func() {
				Expect(cl.Create(ctx, infraClusterWithoutExternallyManagedByAnnotation.DeepCopy())).To(Succeed())
				mustPatchAWSInfraClusterReadiness(infraClusterWithoutExternallyManagedByAnnotation.DeepCopy(), true)
			})

			It("should not change the Status.Ready field", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(
					HaveField("Status.Ready", BeTrue()),
				)
			})
		})

		Context("When the InfraCluster is not Ready", func() {
			BeforeEach(func() {
				Expect(cl.Create(ctx, infraClusterWithoutExternallyManagedByAnnotation.DeepCopy())).To(Succeed())
				mustPatchAWSInfraClusterReadiness(infraClusterWithoutExternallyManagedByAnnotation.DeepCopy(), false)
			})

			It("should not change the Status.Ready field", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(
					HaveField("Status.Ready", BeFalse()),
				)
			})
		})

		It("should update the ClusterOperator status conditions with controller specific ones to reflect a normal state", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())).Should(
				HaveField("Status.Conditions", SatisfyAll(
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerAvailableCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionTrue)),
					)),
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerDegradedCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionFalse)),
					)),
				)),
			)
		})
	})

	Context("When there is an InfraCluster with an externally ManagedBy Annotation with non cluster-capi-operator value", func() {
		Context("When the InfraCluster is Ready", func() {
			BeforeEach(func() {
				Expect(cl.Create(ctx, infraClusterWithExternallyManagedByAnnotationWithValue.DeepCopy())).To(Succeed())
				mustPatchAWSInfraClusterReadiness(infraClusterWithExternallyManagedByAnnotationWithValue.DeepCopy(), true)
			})

			It("should not change the Status.Ready field", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, thirdPartyAnnotation)),
				))
			})
		})
		Context("When the InfraCluster is not Ready", func() {
			BeforeEach(func() {
				Expect(cl.Create(ctx, infraClusterWithExternallyManagedByAnnotationWithValue.DeepCopy())).To(Succeed())
				mustPatchAWSInfraClusterReadiness(infraClusterWithExternallyManagedByAnnotationWithValue.DeepCopy(), false)
			})

			It("should not change the Status.Ready field", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeFalse()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, thirdPartyAnnotation)),
				))
			})
		})
		It("should update the ClusterOperator status conditions with controller specific ones to reflect a normal state", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())).Should(
				HaveField("Status.Conditions", SatisfyAll(
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerAvailableCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionTrue)),
					)),
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerDegradedCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionFalse)),
					)),
				)),
			)
		})
	})

	Context("When there is an InfraCluster with an externally ManagedBy Annotation with cluster-capi-operator value", func() {
		Context("When the InfraCluster is Ready", func() {
			BeforeEach(func() {
				Expect(cl.Create(ctx, infraClusterWithExternallyManagedByAnnotation.DeepCopy())).To(Succeed())
				mustPatchAWSInfraClusterReadiness(infraClusterWithExternallyManagedByAnnotation.DeepCopy(), true)
			})

			It("should not change the Status.Ready field", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
				))
			})
		})
		Context("When the InfraCluster is not Ready", func() {
			BeforeEach(func() {
				Expect(cl.Create(ctx, infraClusterWithExternallyManagedByAnnotation.DeepCopy())).To(Succeed())
				mustPatchAWSInfraClusterReadiness(infraClusterWithExternallyManagedByAnnotation.DeepCopy(), true)
			})

			It("should change the Status.Ready field to true", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
				))
			})
		})
		It("should update the ClusterOperator status conditions with controller specific ones to reflect a normal state", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())).Should(
				HaveField("Status.Conditions", SatisfyAll(
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerAvailableCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionTrue)),
					)),
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.InfraClusterControllerDegradedCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionFalse)),
					)),
				)),
			)
		})
	})
})

func mustPatchAWSInfraClusterReadiness(awsInfraCluster *awsv1.AWSCluster, readiness bool) {
	Eventually(komega.UpdateStatus(awsInfraCluster, func() {
		awsInfraCluster.Status.Ready = readiness
	})).Should(Succeed())
}

func startManager(mgrCtx context.Context, mgrDone chan struct{}, ocpInfra *configv1.Infrastructure) {
	By("Setting up a manager and controller")

	var mgr ctrl.Manager
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Metrics: server.Options{
			BindAddress: "0",
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
		}),
		Controller: config.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

	r := &InfraClusterController{
		ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
			Client: cl,
		},
		Infra:    ocpInfra,
		Platform: ocpInfra.Status.PlatformStatus.Type,
	}

	// TODO: set watch to the right Infra Cluster in setupwithmanager
	Expect(r.SetupWithManager(mgr, &awsv1.AWSCluster{})).To(Succeed(), "Reconciler should be able to setup with manager")

	By("Starting the manager", func() {
		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)

			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	})
}

func stopManager(mgrCancel context.CancelFunc, mgrDone chan struct{}) {
	By("Stopping the manager")
	mgrCancel()
	// Wait for the mgrDone to be closed, which will happen once the mgr has stopped
	<-mgrDone
}
