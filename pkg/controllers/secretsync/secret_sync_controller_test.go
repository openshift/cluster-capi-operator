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
package secretsync

import (
	"bytes"
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	defaultSecretValue = "bar"

	timeout = time.Second * 10
)

var (
	errMissingFormatKey = errors.New("could not find a format key in the worker data secret")
)

func makeUserDataSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}, Data: map[string][]byte{mapiUserDataKey: []byte(defaultSecretValue)}}
}

var _ = Describe("Secret Sync controller: areSecretsEqual reconciler method", func() {
	reconciler := &SecretSyncController{}

	var sourceUserDataSecret *corev1.Secret
	var targetUserDataSecret *corev1.Secret

	var sourceNamespace *corev1.Namespace

	BeforeEach(func() {
		By("Setting up the namespaces for the test")
		sourceNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("managed-").Build()
		Expect(cl.Create(ctx, sourceNamespace)).To(Succeed(), "source namespace should be able to be created")

		sourceUserDataSecret = makeUserDataSecret(managedUserDataSecretName, sourceNamespace.Name)
		targetUserDataSecret = makeUserDataSecret(managedUserDataSecretName, sourceNamespace.Name)
		targetUserDataSecret.Data[capiUserDataKey] = sourceUserDataSecret.Data[mapiUserDataKey]
	})

	It("should return 'true' if Secrets content are equal", func() {
		Expect(reconciler.areSecretsEqual(sourceUserDataSecret, targetUserDataSecret)).Should(BeTrue())
	})

	It("should return 'false' if Secrets content are not equal", func() {
		targetUserDataSecret.Immutable = ptr.To(true)
		Expect(reconciler.areSecretsEqual(sourceUserDataSecret, targetUserDataSecret)).Should(BeFalse())

		targetUserDataSecret.Data = map[string][]byte{}
		Expect(reconciler.areSecretsEqual(sourceUserDataSecret, targetUserDataSecret)).Should(BeFalse())
	})
})

var _ = Describe("Secret Sync controller", func() {
	var rec *record.FakeRecorder

	var mgrCtxCancel context.CancelFunc
	var mgrStopped chan struct{}
	ctx := context.Background()

	var sourceSecret *corev1.Secret

	var reconciler *SecretSyncController

	var managedNamespace *corev1.Namespace
	var sourceNamespace *corev1.Namespace
	var syncedSecretKey client.ObjectKey

	BeforeEach(func() {
		By("Setting up the namespaces for the test")
		managedNamespace = corev1resourcebuilder.Namespace().WithGenerateName("managed-").Build()
		Expect(cl.Create(ctx, managedNamespace)).To(Succeed(), "managed namespace should be able to be created")
		syncedSecretKey = client.ObjectKey{Namespace: managedNamespace.Name, Name: managedUserDataSecretName}

		sourceNamespace = corev1resourcebuilder.Namespace().WithGenerateName("managed-").Build()
		Expect(cl.Create(ctx, sourceNamespace)).To(Succeed(), "source namespace should be able to be created")

		By("Setting up a manager and controller")
		var err error
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		reconciler = &SecretSyncController{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client:           cl,
				Recorder:         rec,
				ManagedNamespace: managedNamespace.Name,
			},
			SourceNamespace: sourceNamespace.Name,
			Scheme:          scheme.Scheme,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		By("Creating needed Secret")
		sourceSecret = makeUserDataSecret(managedUserDataSecretName, sourceNamespace.Name)
		Expect(cl.Create(ctx, sourceSecret)).To(Succeed())

		var mgrCtx context.Context
		mgrCtx, mgrCtxCancel = context.WithCancel(ctx)
		mgrStopped = make(chan struct{})

		By("Creating the ClusterOperator")
		co := &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: controllers.ClusterOperatorName,
			},
		}
		Expect(cl.Create(context.Background(), co.DeepCopy())).To(Succeed())

		By("Starting the manager")
		go func() {
			defer GinkgoRecover()
			defer close(mgrStopped)

			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	})

	AfterEach(func() {
		By("Closing the manager")
		mgrCtxCancel()
		Eventually(mgrStopped, timeout).Should(BeClosed())

		By("Cleaning up test resources")
		testutils.CleanupResources(Default, ctx, cfg, cl, sourceNamespace.Name, &corev1.Secret{})
		testutils.CleanupResources(Default, ctx, cfg, cl, managedNamespace.Name, &corev1.Secret{}, &configv1.ClusterOperator{})

		sourceSecret = nil
	})

	It("secret should be synced up after first reconcile", func() {
		Eventually(func() (bool, error) {
			syncedUserDataSecret := &corev1.Secret{}
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}

			formatValue, ok := syncedUserDataSecret.Data["format"]
			if !ok {
				return false, errMissingFormatKey
			}
			Expect(string(formatValue)).Should(Equal("ignition"))

			return bytes.Equal(syncedUserDataSecret.Data[capiUserDataKey], []byte(defaultSecretValue)), nil
		}, timeout).Should(BeTrue())
	})

	It("secret should be synced up if managed user data secret changed", func() {
		changedSourceSecret := sourceSecret.DeepCopy()
		changedSourceSecret.Data = map[string][]byte{mapiUserDataKey: []byte("managed one changed")}
		Expect(cl.Update(ctx, changedSourceSecret)).To(Succeed())

		Eventually(func() (bool, error) {
			syncedUserDataSecret := &corev1.Secret{}
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}

			formatValue, ok := syncedUserDataSecret.Data["format"]
			if !ok {
				return false, errMissingFormatKey
			}
			Expect(string(formatValue)).Should(Equal("ignition"))

			return bytes.Equal(syncedUserDataSecret.Data[capiUserDataKey], []byte("managed one changed")), nil
		}, timeout).Should(BeTrue())
	})

	It("secret should be synced up if owned user data secret is deleted or changed", func() {
		syncedUserDataSecret := &corev1.Secret{}
		Eventually(func() error {
			return cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
		}, timeout).Should(Succeed())

		syncedUserDataSecret.Data = map[string][]byte{capiUserDataKey: []byte("baz")}
		Expect(cl.Update(ctx, syncedUserDataSecret)).To(Succeed())
		Eventually(func() (bool, error) {
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}

			formatValue, ok := syncedUserDataSecret.Data["format"]
			if !ok {
				return false, errMissingFormatKey
			}
			Expect(string(formatValue)).Should(Equal("ignition"))

			return bytes.Equal(syncedUserDataSecret.Data[capiUserDataKey], []byte(defaultSecretValue)), nil
		}, timeout).Should(BeTrue())
	})

	It("secret not be updated if source and target secret contents are identical", func() {
		syncedUserDataSecret := &corev1.Secret{}
		Eventually(func() error {
			return cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
		}, timeout).Should(Succeed())
		initialSecretresourceVersion := syncedUserDataSecret.ResourceVersion

		Expect(cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)).Should(Succeed())
		Expect(initialSecretresourceVersion).Should(BeEquivalentTo(syncedUserDataSecret.ResourceVersion))
	})

	It("should updated the ClusterOperator status conditions with controller specific ones to reflect a normal state", func() {
		Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build()), timeout).
			Should(
				HaveField("Status.Conditions", SatisfyAll(
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.SecretSyncControllerAvailableCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionTrue)),
					)),
					ContainElement(And(
						HaveField("Type", BeEquivalentTo(operatorstatus.SecretSyncControllerDegradedCondition)),
						HaveField("Status", BeEquivalentTo(configv1.ConditionFalse)),
					)),
				)),
			)
	})
})
