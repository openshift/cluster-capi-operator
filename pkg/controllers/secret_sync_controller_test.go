package controllers

import (
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	defaultSecretKey   = "foo"
	defaultSecretValue = "bar"

	timeout = time.Second * 10
)

func makeUserDataSecret() *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      managedUserDataSecretName,
		Namespace: SecretSourceNamespace,
	}, Data: map[string][]byte{defaultSecretKey: []byte(defaultSecretValue)}}
}

var _ = Describe("areSecretsEqual reconciler method", func() {
	reconciler := &UserDataSecretController{}

	It("should return 'true' if Secrets content are equal", func() {
		Expect(reconciler.areSecretsEqual(makeUserDataSecret(), makeUserDataSecret())).Should(BeTrue())
	})

	It("should return 'false' if Secrets content are not equal", func() {
		changedManagedUserDataSecret := makeUserDataSecret()
		changedManagedUserDataSecret.Immutable = pointer.Bool(true)
		Expect(reconciler.areSecretsEqual(changedManagedUserDataSecret, makeUserDataSecret())).Should(BeFalse())

		changedManagedUserDataSecret = makeUserDataSecret()
		changedManagedUserDataSecret.Data = map[string][]byte{}
		Expect(reconciler.areSecretsEqual(changedManagedUserDataSecret, makeUserDataSecret())).Should(BeFalse())
	})
})

var _ = Describe("User Data Secret controller", func() {
	var rec *record.FakeRecorder

	var mgrCtxCancel context.CancelFunc
	var mgrStopped chan struct{}
	ctx := context.Background()

	var sourceSecret *corev1.Secret

	var reconciler *UserDataSecretController

	syncedSecretKey := client.ObjectKey{Namespace: testManagedNamespace, Name: managedUserDataSecretName}

	BeforeEach(func() {
		By("Setting up a new manager")
		mgr, err := manager.New(cfg, manager.Options{MetricsBindAddress: "0"})
		Expect(err).NotTo(HaveOccurred())

		reconciler = &UserDataSecretController{
			ClusterOperatorStatusClient: ClusterOperatorStatusClient{
				Client:           cl,
				Recorder:         rec,
				ManagedNamespace: testManagedNamespace,
			},
			Scheme: scheme.Scheme,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		By("Creating needed Secret")
		sourceSecret = makeUserDataSecret()
		Expect(cl.Create(ctx, sourceSecret)).To(Succeed())

		var mgrCtx context.Context
		mgrCtx, mgrCtxCancel = context.WithCancel(ctx)
		mgrStopped = make(chan struct{})

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

		co := &configv1.ClusterOperator{}
		err := cl.Get(context.Background(), client.ObjectKey{Name: clusterOperatorName}, co)
		if err == nil || !apierrors.IsNotFound(err) {
			Eventually(func() bool {
				err := cl.Delete(context.Background(), co)
				return err == nil || apierrors.IsNotFound(err)
			}).Should(BeTrue())
		}
		Eventually(apierrors.IsNotFound(cl.Get(context.Background(), client.ObjectKey{Name: clusterOperatorName}, co))).Should(BeTrue())

		By("Cleanup resources")
		deleteOptions := &client.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		}

		allSecrets := &corev1.SecretList{}
		Expect(cl.List(ctx, allSecrets)).To(Succeed())
		for _, cm := range allSecrets.Items {
			Expect(cl.Delete(ctx, cm.DeepCopy(), deleteOptions)).To(Succeed())
			Eventually(
				apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(cm.DeepCopy()), &corev1.ConfigMap{})),
			).Should(BeTrue())
		}

		sourceSecret = nil

		// Creating the cluster api operator
		co = &configv1.ClusterOperator{}
		co.SetName(clusterOperatorName)
		Expect(cl.Create(context.Background(), co.DeepCopy())).To(Succeed())
	})

	It("secret should be synced up after first reconcile", func() {
		Eventually(func() (bool, error) {
			syncedUserDataSecret := &corev1.Secret{}
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}
			return bytes.Equal(syncedUserDataSecret.Data[defaultSecretKey], []byte(defaultSecretValue)), nil
		}).Should(BeTrue())
	})

	It("secret should be synced up if managed user data secret changed", func() {
		changedSourceSecret := sourceSecret.DeepCopy()
		changedSourceSecret.Data = map[string][]byte{defaultSecretKey: []byte("managed one changed")}
		Expect(cl.Update(ctx, changedSourceSecret)).To(Succeed())

		Eventually(func() (bool, error) {
			syncedUserDataSecret := &corev1.Secret{}
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}
			return bytes.Equal(syncedUserDataSecret.Data[defaultSecretKey], []byte("managed one changed")), nil
		}).Should(BeTrue())
	})

	It("secret should be synced up if owned user data secret is deleted or changed", func() {
		syncedUserDataSecret := &corev1.Secret{}
		Eventually(func() error {
			return cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
		}, timeout).Should(Succeed())

		syncedUserDataSecret.Data = map[string][]byte{defaultSecretKey: []byte("baz")}
		Expect(cl.Update(ctx, syncedUserDataSecret)).To(Succeed())
		Eventually(func() (bool, error) {
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}
			return bytes.Equal(syncedUserDataSecret.Data[defaultSecretKey], []byte(defaultSecretValue)), nil
		}).Should(BeTrue())

		Expect(cl.Delete(ctx, syncedUserDataSecret)).To(Succeed())
		Eventually(func() (bool, error) {
			err := cl.Get(ctx, syncedSecretKey, syncedUserDataSecret)
			if err != nil {
				return false, err
			}
			return bytes.Equal(syncedUserDataSecret.Data[defaultSecretKey], []byte(defaultSecretValue)), nil
		}).Should(BeTrue())
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
})
