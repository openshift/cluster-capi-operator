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

package machinesync

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	awsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/rest"

	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ = Describe("AWS load balancer validation during MAPI->CAPI conversion", func() {
	var (
		mgrCancel  context.CancelFunc
		mgrDone    chan struct{}
		mgr        manager.Manager
		reconciler *MachineSyncReconciler

		awsClusterBuilder awsv1resourcebuilder.AWSClusterBuilder

		capiNamespace *corev1.Namespace
		mapiNamespace *corev1.Namespace

		k komega.Komega

		infrastructureName string
	)

	startManager := func(mgr *manager.Manager) (context.CancelFunc, chan struct{}) {
		mgrCtx, mgrCancel := context.WithCancel(context.Background())
		mgrDone := make(chan struct{})

		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)

			Expect((*mgr).Start(mgrCtx)).To(Succeed())
		}()

		return mgrCancel, mgrDone
	}

	stopManager := func() {
		mgrCancel()
		<-mgrDone
	}

	BeforeEach(func() {
		k = komega.New(k8sClient)

		mapiNamespace = corev1resourcebuilder.Namespace().WithGenerateName("openshift-machine-api-").Build()
		capiNamespace = corev1resourcebuilder.Namespace().WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed())
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed())

		infrastructureName = "cluster-aws-lb"
		awsClusterBuilder = awsv1resourcebuilder.AWSCluster().WithNamespace(capiNamespace.GetName()).WithName(infrastructureName)

		// Create CAPI Cluster that all tests will use
		capiCluster := clusterv1resourcebuilder.Cluster().WithNamespace(capiNamespace.GetName()).WithName(infrastructureName).WithInfrastructureRef(clusterv1.ContractVersionedObjectReference{
			APIGroup: "infrastructure.cluster.x-k8s.io",
			Kind:     "AWSCluster",
			Name:     infrastructureName,
		}).Build()
		Expect(k8sClient.Create(ctx, capiCluster)).To(Succeed())

		var err error
		var controllerCfg *rest.Config
		controllerCfg, err = testEnv.ControlPlane.APIServer.SecureServing.AddUser(
			envtest.User{
				Name:   "system:serviceaccount:openshift-cluster-api:cluster-capi-operator",
				Groups: []string{"system:masters", "system:authenticated"},
			}, cfg)
		Expect(err).ToNot(HaveOccurred())

		mgr, err = ctrl.NewManager(controllerCfg, ctrl.Options{
			Scheme: testScheme,
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred())

		infra := configv1resourcebuilder.Infrastructure().AsAWS("cluster", "us-east-1").WithInfrastructureName(infrastructureName).Build()
		infraTypes, _, err := util.GetCAPITypesForInfrastructure(infra)
		Expect(err).ToNot(HaveOccurred(), "InfraTypes should be able to be created")

		reconciler = &MachineSyncReconciler{
			Client:        mgr.GetClient(),
			Infra:         infra,
			Platform:      configv1.AWSPlatformType,
			InfraTypes:    infraTypes,
			CAPINamespace: capiNamespace.GetName(),
			MAPINamespace: mapiNamespace.GetName(),
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		mgrCancel, mgrDone = startManager(&mgr)
	})

	AfterEach(func() {
		stopManager()

		// Cleanup created resources in test namespaces
		Expect(k8sClient.DeleteAllOf(ctx, &clusterv1.Machine{}, client.InNamespace(capiNamespace.GetName()))).To(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &awsv1.AWSMachine{}, client.InNamespace(capiNamespace.GetName()))).To(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &awsv1.AWSCluster{}, client.InNamespace(capiNamespace.GetName()))).To(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &clusterv1.Cluster{}, client.InNamespace(capiNamespace.GetName()))).To(Succeed())
		Expect(k8sClient.Delete(ctx, mapiNamespace)).To(Succeed())
		Expect(k8sClient.Delete(ctx, capiNamespace)).To(Succeed())
	})

	It("rejects worker machines that define load balancers", func() {
		loadBalancerSpec := &awsv1.AWSLoadBalancerSpec{
			Name:             ptr.To("cluster-int"),
			LoadBalancerType: awsv1.LoadBalancerTypeNLB,
		}

		awsCluster := awsClusterBuilder.WithControlPlaneLoadBalancer(loadBalancerSpec).Build()
		Expect(k8sClient.Create(ctx, awsCluster)).To(Succeed())

		lbRefs := []mapiv1beta1.LoadBalancerReference{
			{Name: "cluster-int", Type: mapiv1beta1.NetworkLoadBalancerType},
		}

		mapiMachine := machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.GetName()).
			WithGenerateName("worker-").
			AsWorker().
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(lbRefs)).
			Build()

		Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed())
		Eventually(k.UpdateStatus(mapiMachine, func() { mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI })).Should(Succeed())

		Eventually(k.Object(mapiMachine), timeout).Should(
			HaveField("Status.Conditions", ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(consts.SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionFalse)),
					HaveField("Reason", Equal("FailedToConvertMAPIMachineToCAPI")),
					HaveField("Message", ContainSubstring("loadBalancers are not supported for non-control plane machines")),
				))),
		)
	})

	It("rejects control-plane machines missing required control-plane LB", func() {
		loadBalancerSpec := &awsv1.AWSLoadBalancerSpec{
			Name:             ptr.To("cluster-int"),
			LoadBalancerType: awsv1.LoadBalancerTypeNLB,
		}
		awsCluster := awsClusterBuilder.WithControlPlaneLoadBalancer(loadBalancerSpec).Build()
		Expect(k8sClient.Create(ctx, awsCluster)).To(Succeed())

		mapiMachine := machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.GetName()).
			WithGenerateName("master-").
			AsMaster().
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
			Build()

		Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed())
		Eventually(k.UpdateStatus(mapiMachine, func() { mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI })).Should(Succeed())

		Eventually(k.Object(mapiMachine), timeout).Should(
			HaveField("Status.Conditions", ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(consts.SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionFalse)),
					HaveField("Reason", Equal("FailedToConvertMAPIMachineToCAPI")),
					HaveField("Message", ContainSubstring("must include load balancer named \"cluster-int\"")),
				))),
		)
	})

	It("rejects control-plane machines with wrong LB type", func() {
		loadBalancerSpec := &awsv1.AWSLoadBalancerSpec{
			Name:             ptr.To("cluster-int"),
			LoadBalancerType: awsv1.LoadBalancerTypeNLB,
		}
		extraLoadBalancerSpec := &awsv1.AWSLoadBalancerSpec{
			Name:             ptr.To("cluster-ext"),
			LoadBalancerType: awsv1.LoadBalancerTypeNLB,
		}
		awsCluster := awsClusterBuilder.WithControlPlaneLoadBalancer(loadBalancerSpec).WithSecondaryControlPlaneLoadBalancer(extraLoadBalancerSpec).Build()
		Expect(k8sClient.Create(ctx, awsCluster)).To(Succeed())

		// Provide wrong type for cluster-int and an extra unexpected lb
		lbRefs := []mapiv1beta1.LoadBalancerReference{
			{Name: "cluster-int", Type: mapiv1beta1.ClassicLoadBalancerType},
			{Name: "unexpected", Type: mapiv1beta1.NetworkLoadBalancerType},
			// Purposely omit cluster-ext to also trigger missing secondary error
		}

		mapiMachine := machinev1resourcebuilder.Machine().AsMaster().
			WithNamespace(mapiNamespace.GetName()).
			WithGenerateName("master-").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(lbRefs)).
			Build()

		Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed())
		Eventually(k.UpdateStatus(mapiMachine, func() { mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI })).Should(Succeed())

		Eventually(k.Object(mapiMachine), timeout).Should(
			HaveField("Status.Conditions", ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(consts.SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionFalse)),
					HaveField("Reason", Equal("FailedToConvertMAPIMachineToCAPI")),
					HaveField("Message", SatisfyAll(
						ContainSubstring("load balancer \"cluster-int\" must be of type \"network\""),
						ContainSubstring("must include load balancer named \"cluster-ext\""),
						ContainSubstring("unexpected load balancer \"unexpected\" defined on machine"),
					)),
				))),
		)
	})

	It("accepts control-plane machines with matching LB names and types", func() {
		loadBalancerSpec := &awsv1.AWSLoadBalancerSpec{
			Name:             ptr.To("cluster-int"),
			LoadBalancerType: awsv1.LoadBalancerTypeNLB,
		}
		secondaryLoadBalancerSpec := &awsv1.AWSLoadBalancerSpec{
			Name:             ptr.To("cluster-ext"),
			LoadBalancerType: awsv1.LoadBalancerTypeNLB,
		}
		awsCluster := awsClusterBuilder.WithControlPlaneLoadBalancer(loadBalancerSpec).WithSecondaryControlPlaneLoadBalancer(secondaryLoadBalancerSpec).Build()
		Expect(k8sClient.Create(ctx, awsCluster)).To(Succeed())

		lbRefs := []mapiv1beta1.LoadBalancerReference{
			{Name: "cluster-int", Type: mapiv1beta1.NetworkLoadBalancerType},
			{Name: "cluster-ext", Type: mapiv1beta1.NetworkLoadBalancerType},
		}

		mapiMachine := machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.GetName()).
			WithGenerateName("master-").
			AsMaster().
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(lbRefs)).
			Build()

		Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed())
		Eventually(k.UpdateStatus(mapiMachine, func() { mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI })).Should(Succeed())

		// Expect success condition
		Eventually(k.Object(mapiMachine), timeout).Should(
			HaveField("Status.Conditions", ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(consts.SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionTrue)),
					HaveField("Reason", Equal("ResourceSynchronized")),
					HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
				))),
		)

		// And that a CAPI machine has been created
		capiMachine := clusterv1resourcebuilder.Machine().WithNamespace(capiNamespace.GetName()).WithName(mapiMachine.GetName()).Build()
		Eventually(k8sClient.Get(ctx, client.ObjectKeyFromObject(capiMachine), capiMachine), timeout).Should(Succeed())
	})
})

var _ = Describe("validateLoadBalancerReferencesAgainstExpected", func() {
	var (
		lbfieldPath *field.Path
	)

	BeforeEach(func() {
		lbfieldPath = field.NewPath("spec", "providerSpec", "value", "loadBalancers")
	})

	type validateLoadBalancerMatchTableInput struct {
		actualLoadBalancers   []mapiv1beta1.LoadBalancerReference
		expectedLoadBalancers map[string]mapiv1beta1.AWSLoadBalancerType
		expectedErrorMessages []string
	}

	DescribeTable("validate load balancer matching",
		func(in validateLoadBalancerMatchTableInput) {
			err := validateLoadBalancerReferencesAgainstExpected(in.actualLoadBalancers, in.expectedLoadBalancers, lbfieldPath)

			if len(in.expectedErrorMessages) > 0 {
				Expect(err).ToNot(BeNil())
				for _, expectedMsg := range in.expectedErrorMessages {
					Expect(err.Error()).To(ContainSubstring(expectedMsg))
				}
			} else {
				Expect(err).To(BeNil())
			}
		},
		Entry("should succeed when actual and expected load balancers match perfectly", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "cluster-int", Type: mapiv1beta1.NetworkLoadBalancerType},
				{Name: "cluster-ext", Type: mapiv1beta1.ClassicLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
				"cluster-ext": mapiv1beta1.ClassicLoadBalancerType,
			},
		}),
		Entry("should succeed when load balancers are in different order", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "cluster-ext", Type: mapiv1beta1.ClassicLoadBalancerType},
				{Name: "cluster-int", Type: mapiv1beta1.NetworkLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
				"cluster-ext": mapiv1beta1.ClassicLoadBalancerType,
			},
		}),
		Entry("should fail when an unexpected load balancer is present", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "cluster-int", Type: mapiv1beta1.NetworkLoadBalancerType},
				{Name: "unexpected-lb", Type: mapiv1beta1.NetworkLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
			},
			expectedErrorMessages: []string{"unexpected load balancer \"unexpected-lb\" defined on machine"},
		}),
		Entry("should fail when a required load balancer is missing", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "cluster-int", Type: mapiv1beta1.NetworkLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
				"cluster-ext": mapiv1beta1.ClassicLoadBalancerType,
			},
			expectedErrorMessages: []string{"must include load balancer named \"cluster-ext\""},
		}),
		Entry("should fail when load balancer type is incorrect", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "cluster-int", Type: mapiv1beta1.ClassicLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
			},
			expectedErrorMessages: []string{"load balancer \"cluster-int\" must be of type \"network\" to match AWSCluster"},
		}),
		Entry("should report multiple errors when multiple issues are present", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "cluster-int", Type: mapiv1beta1.ClassicLoadBalancerType},
				{Name: "unexpected-lb", Type: mapiv1beta1.NetworkLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
				"cluster-ext": mapiv1beta1.ClassicLoadBalancerType,
			},
			expectedErrorMessages: []string{
				"load balancer \"cluster-int\" must be of type \"network\" to match AWSCluster",
				"unexpected load balancer \"unexpected-lb\" defined on machine",
				"must include load balancer named \"cluster-ext\"",
			},
		}),
		Entry("should succeed when both actual and expected are empty", validateLoadBalancerMatchTableInput{
			actualLoadBalancers:   []mapiv1beta1.LoadBalancerReference{},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{},
		}),
		Entry("should fail when actual is empty but expected has values", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{
				"cluster-int": mapiv1beta1.NetworkLoadBalancerType,
			},
			expectedErrorMessages: []string{"must include load balancer named \"cluster-int\""},
		}),
		Entry("should fail when expected is empty but actual has values", validateLoadBalancerMatchTableInput{
			actualLoadBalancers: []mapiv1beta1.LoadBalancerReference{
				{Name: "unexpected-lb", Type: mapiv1beta1.NetworkLoadBalancerType},
			},
			expectedLoadBalancers: map[string]mapiv1beta1.AWSLoadBalancerType{},
			expectedErrorMessages: []string{"unexpected load balancer \"unexpected-lb\" defined on machine"},
		}),
	)
})
