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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"

	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	mapiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1"
	mapiv1beta1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
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
	var capiNamespace *corev1.Namespace
	var mapiNamespace *corev1.Namespace

	ocpInfraClusterName := "test-infra-cluster-name"
	ocpInfraAWS := configv1resourcebuilder.Infrastructure().AsAWS(ocpInfraClusterName, awsTestRegion).Build()

	infraClusterWithExternallyManagedByAnnotation := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: ocpInfraClusterName,
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
			Name: ocpInfraClusterName,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: thirdPartyAnnotation,
			},
		},
	}

	infraClusterWithoutExternallyManagedByAnnotation := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: ocpInfraClusterName,
		},
	}

	BeforeEach(func() {
		// Create ClusterOperator.
		Expect(cl.Create(ctx, configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())).To(Succeed())

		// Create MAPI and CAPI namespaces for the test.
		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(cl.Create(ctx, mapiNamespace)).To(Succeed(), "MAPI namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(cl.Create(ctx, capiNamespace)).To(Succeed(), "CAPI namespace should be able to be created")

		bareInfraCluster = &awsv1.AWSCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ocpInfraClusterName,
				Namespace: capiNamespace.Name,
			},
		}

		infraClusterWithExternallyManagedByAnnotation.Namespace = capiNamespace.Name
		infraClusterWithExternallyManagedByAnnotationWithValue.Namespace = capiNamespace.Name
		infraClusterWithoutExternallyManagedByAnnotation.Namespace = capiNamespace.Name

		// Setup and Start Manager.
		mgrCtx, mgrCancel = context.WithCancel(context.Background())
		mgrDone = make(chan struct{})
		startManager(mgrCtx, mgrDone, ocpInfraAWS, capiNamespace.Name, mapiNamespace.Name)
	})

	AfterEach(func() {
		// Stop Manager.
		stopManager(mgrCancel, mgrDone)
		// Cleanup Resources.
		testutils.CleanupResources(Default, ctx, cfg, cl, "", &configv1.ClusterOperator{})
		testutils.CleanupResources(Default, ctx, cfg, cl, capiNamespace.Name, &awsv1.AWSCluster{})
		testutils.CleanupResources(Default, ctx, cfg, cl, mapiNamespace.Name, &mapiv1beta1.Machine{})
	})

	Context("When there is no InfraCluster", func() {
		Context("When there is an active ControlPlaneMachineSet", func() {
			BeforeEach(func() {
				internalAndExternalLB := []mapiv1beta1.LoadBalancerReference{{Name: "testClusterID-ext", Type: mapiv1beta1.NetworkLoadBalancerType}, {Name: "testClusterID-int", Type: mapiv1beta1.NetworkLoadBalancerType}}

				machineTemplateBuilder := mapiv1resourcebuilder.OpenShiftMachineV1Beta1Template().WithProviderSpecBuilder(
					mapiv1beta1resourcebuilder.AWSProviderSpec().WithLoadBalancers(internalAndExternalLB),
				)
				cpms := mapiv1resourcebuilder.ControlPlaneMachineSet().WithNamespace(mapiNamespace.Name).WithName("cluster").WithMachineTemplateBuilder(machineTemplateBuilder).Build()

				Expect(cl.Create(ctx, cpms)).To(Succeed())
			})

			It("should create an InfraCluster, with Ready: true and externally ManagedBy Annotation", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
				))
			})

			Context("When there is a ControlPlaneLoadBalancer and a SecondaryControlPlaneLoadBalancer", func() {
				It("should order two load balancers preferring '-int' as primary", func() {
					internalLB := &awsv1.AWSLoadBalancerSpec{Name: ptr.To("testClusterID-int"), LoadBalancerType: awsv1.LoadBalancerTypeNLB, Scheme: &awsv1.ELBSchemeInternal}
					externalLB := &awsv1.AWSLoadBalancerSpec{Name: ptr.To("testClusterID-ext"), LoadBalancerType: awsv1.LoadBalancerTypeNLB, Scheme: &awsv1.ELBSchemeInternetFacing}

					Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
						HaveField("Spec.ControlPlaneLoadBalancer", Equal(internalLB)),
						HaveField("Spec.SecondaryControlPlaneLoadBalancer", Equal(externalLB)),
					))
				})
			})
		})

		Context("When there are Control Plane Machines but no ControlPlaneMachineSet", func() {
			youngLoadBalancers := []mapiv1beta1.LoadBalancerReference{{Name: "young-int", Type: mapiv1beta1.NetworkLoadBalancerType}}
			oldLoadBalancers := []mapiv1beta1.LoadBalancerReference{{Name: "old-int", Type: mapiv1beta1.NetworkLoadBalancerType}}
			BeforeEach(func() {
				machine1 := mapiv1beta1resourcebuilder.Machine().AsMaster().WithNamespace(mapiNamespace.Name).WithName("master-1").WithProviderSpecBuilder(mapiv1beta1resourcebuilder.AWSProviderSpec().WithLoadBalancers(oldLoadBalancers)).Build()
				machine2 := mapiv1beta1resourcebuilder.Machine().AsMaster().WithNamespace(mapiNamespace.Name).WithName("master-2").WithProviderSpecBuilder(mapiv1beta1resourcebuilder.AWSProviderSpec().WithLoadBalancers(youngLoadBalancers)).Build()
				machine3 := mapiv1beta1resourcebuilder.Machine().AsMaster().WithNamespace(mapiNamespace.Name).WithName("master-3").WithProviderSpecBuilder(mapiv1beta1resourcebuilder.AWSProviderSpec().WithLoadBalancers(oldLoadBalancers)).Build()
				Expect(cl.Create(ctx, machine1)).To(Succeed())
				Expect(cl.Create(ctx, machine3)).To(Succeed())

				// Create machine2 after machine3 with a delay to ensure machine2 is the youngest machine.
				time.Sleep(1 * time.Second)
				Expect(cl.Create(ctx, machine2)).To(Succeed())

				// Validate the creationtimestamp of the machines
				Expect(machine3.CreationTimestamp.Time).To(BeTemporally("<", machine2.CreationTimestamp.Time))
				Expect(machine1.CreationTimestamp.Time).To(BeTemporally("<", machine2.CreationTimestamp.Time))

				// Delete infraCluster for it to be recreated after all the machines are created.
				// In real cluster the machines will be already present when the InfraCluster Controller is started.
				Expect(cl.Delete(ctx, bareInfraCluster)).To(Succeed())
			})

			It("should create an InfraCluster, with Ready: true and externally ManagedBy Annotation", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
				))
			})

			It("should have the load balancer configuration derived from the youngest machine", func() {
				internalLB := &awsv1.AWSLoadBalancerSpec{Name: ptr.To("young-int"), LoadBalancerType: awsv1.LoadBalancerTypeNLB, Scheme: &awsv1.ELBSchemeInternal}

				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Spec.ControlPlaneLoadBalancer", Equal(internalLB)),
					HaveField("Spec.SecondaryControlPlaneLoadBalancer", BeNil()),
				))
			})

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
				mustPatchAWSInfraClusterReadiness(infraClusterWithExternallyManagedByAnnotation.DeepCopy(), false)
			})

			It("should change the Status.Ready field to true", func() {
				Eventually(komega.Object(bareInfraCluster)).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
					HaveField("Annotations", HaveKeyWithValue(clusterv1.ManagedByAnnotation, managedByAnnotationValueClusterCAPIOperatorInfraClusterController)),
				))
			})
		})
	})

})

func mustPatchAWSInfraClusterReadiness(awsInfraCluster *awsv1.AWSCluster, readiness bool) {
	Eventually(komega.UpdateStatus(awsInfraCluster, func() {
		awsInfraCluster.Status.Ready = readiness
	})).Should(Succeed())
}

func startManager(mgrCtx context.Context, mgrDone chan struct{}, ocpInfra *configv1.Infrastructure, capiNamespace string, mapiNamespace string) {
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
			Client:           cl,
			ManagedNamespace: capiNamespace,
		},
		Infra:         ocpInfra,
		Platform:      ocpInfra.Status.PlatformStatus.Type,
		CAPINamespace: capiNamespace,
		MAPINamespace: mapiNamespace,
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
