package clusteroperator

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var (
	operatorImageName                 = "cluster-kube-cluster-api-operator"
	operatorImageSource               = "test.com/operator:tag"
	kubeRBACProxyImageName            = "kube-rbac-proxy"
	kubeRBACProxySource               = "test.com/kube-rbac-proxy:tag"
	coreProviderImageName             = "cluster-capi-controllers"
	coreProviderImageSource           = "test.com/cluster-api:tag"
	infrastructureProviderImageName   = "aws-cluster-api-controllers"
	infrastructureProviderImageSource = "test.com/cluster-api-provider-aws:tag"
)

var _ = Describe("Reconcile components", func() {
	var r *ClusterOperatorReconciler

	ctx := context.Background()
	providerSpec := operatorv1.ProviderSpec{
		Version: "v1.0.0",
		Deployment: &operatorv1.DeploymentSpec{
			Containers: []operatorv1.ContainerSpec{
				{
					Name: "manager",
					Image: &operatorv1.ImageMeta{
						Name:       "test",
						Repository: "image.com",
						Tag:        "tag",
					},
				},
			},
		},
	}

	BeforeEach(func() {
		r = &ClusterOperatorReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			Images: map[string]string{
				operatorImageName:               operatorImageSource,
				kubeRBACProxyImageName:          kubeRBACProxySource,
				coreProviderImageName:           coreProviderImageSource,
				infrastructureProviderImageName: infrastructureProviderImageSource,
			},
		}
	})

	Context("reconcile operator deployment", func() {
		var deployment *appsv1.Deployment

		BeforeEach(func() {
			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-api-operator",
					Namespace: controllers.DefaultManagedNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"test": "test"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"test": "test"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "manager",
									Image: "image.com/test:tag",
								},
								{
									Name:  "kube-rbac-proxy",
									Image: "image.com/test2:tag",
								},
							},
						},
					},
				},
			}
		})

		AfterEach(func() {
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      deployment.Name,
				Namespace: deployment.Namespace,
			}, deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(operatorImageSource))
			Expect(deployment.Spec.Template.Spec.Containers[1].Image).To(Equal(kubeRBACProxySource))
			Expect(test.CleanupAndWait(ctx, cl, deployment)).To(Succeed())
		})

		It("should create a deployment and modify images", func() {
			Expect(r.reconcileOperatorDeployment(ctx, deployment)).To(Succeed())
		})

		It("should update an existing deployment", func() {
			Expect(cl.Create(ctx, deployment)).To(Succeed())
			Expect(r.reconcileOperatorDeployment(ctx, deployment)).To(Succeed())
		})
	})

	Context("reconcile operator service", func() {
		var service *corev1.Service

		BeforeEach(func() {
			service = &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-api-operator",
					Namespace: controllers.DefaultManagedNamespace,
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"test": "test"},
					Ports: []corev1.ServicePort{
						{
							Name: "http",
							Port: 80,
						},
					},
				},
			}
		})

		AfterEach(func() {
			Expect(test.CleanupAndWait(ctx, cl, service)).To(Succeed())
		})

		It("should create a service", func() {
			Expect(r.reconcileOperatorService(ctx, service)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      service.Name,
				Namespace: service.Namespace,
			}, service)).To(Succeed())
			Expect(service.Spec.Selector).To(Equal(map[string]string{"test": "test"}))
		})

		It("should update an existing service", func() {
			Expect(cl.Create(ctx, service)).To(Succeed())
			service.Spec.Selector = map[string]string{"test": "test2"}
			Expect(r.reconcileOperatorService(ctx, service)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      service.Name,
				Namespace: service.Namespace,
			}, service)).To(Succeed())
			Expect(service.Spec.Selector).To(Equal(map[string]string{"test": "test2"}))
		})
	})

	Context("reconcile core provider", func() { // nolint:dupl
		var coreProvider *operatorv1.CoreProvider

		BeforeEach(func() {
			coreProvider = &operatorv1.CoreProvider{
				TypeMeta: metav1.TypeMeta{
					Kind: "CoreProvider",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-api",
					Namespace: controllers.DefaultManagedNamespace,
				},
				Spec: operatorv1.CoreProviderSpec{
					ProviderSpec: providerSpec,
				},
			}
		})

		AfterEach(func() {
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      coreProvider.Name,
				Namespace: coreProvider.Namespace,
			}, coreProvider)).To(Succeed())
			imageMeta := newImageMeta(coreProviderImageSource)
			Expect(coreProvider.Spec.ProviderSpec.Deployment.Containers).To(HaveLen(1))
			Expect(coreProvider.Spec.ProviderSpec.Deployment.Containers[0].Image.Name).To(Equal(imageMeta.Name))
			Expect(coreProvider.Spec.ProviderSpec.Deployment.Containers[0].Image.Repository).To(Equal(imageMeta.Repository))
			Expect(coreProvider.Spec.ProviderSpec.Deployment.Containers[0].Image.Tag).To(Equal(imageMeta.Tag))

			Expect(test.CleanupAndWait(ctx, cl, coreProvider)).To(Succeed())
		})

		It("should create core provider and modify container images", func() {
			Expect(r.reconcileCoreProvider(ctx, coreProvider)).To(Succeed())
		})

		It("should update an existing core provider", func() {
			Expect(cl.Create(ctx, coreProvider)).To(Succeed())
			coreProvider.TypeMeta.Kind = "CoreProvider" // kind gets erased after Create()
			coreProvider.Spec.Version = "v2.0.0"
			Expect(r.reconcileCoreProvider(ctx, coreProvider)).To(Succeed())
			Expect(coreProvider.Spec.Version).To(Equal("v2.0.0"))
		})
	})

	Context("reconcile infrastructure provider", func() { // nolint:dupl
		var infraProvider *operatorv1.InfrastructureProvider

		BeforeEach(func() {
			infraProvider = &operatorv1.InfrastructureProvider{
				TypeMeta: metav1.TypeMeta{
					Kind: "InfrastructureProvider",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "aws",
					Namespace: controllers.DefaultManagedNamespace,
				},
				Spec: operatorv1.InfrastructureProviderSpec{
					ProviderSpec: providerSpec,
				},
			}
		})

		AfterEach(func() {
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      infraProvider.Name,
				Namespace: infraProvider.Namespace,
			}, infraProvider)).To(Succeed())
			Expect(infraProvider.Spec.ProviderSpec.Deployment.Containers).To(HaveLen(1))
			imageMeta := newImageMeta(infrastructureProviderImageSource)
			Expect(infraProvider.Spec.ProviderSpec.Deployment.Containers[0].Image.Name).To(Equal(imageMeta.Name))
			Expect(infraProvider.Spec.ProviderSpec.Deployment.Containers[0].Image.Repository).To(Equal(imageMeta.Repository))
			Expect(infraProvider.Spec.ProviderSpec.Deployment.Containers[0].Image.Tag).To(Equal(imageMeta.Tag))

			Expect(test.CleanupAndWait(ctx, cl, infraProvider)).To(Succeed())
		})

		It("should create infra provider and modify container images", func() {
			Expect(r.reconcileInfrastructureProvider(ctx, infraProvider)).To(Succeed())
		})

		It("should update an existing infra provider", func() {
			Expect(cl.Create(ctx, infraProvider)).To(Succeed())
			infraProvider.TypeMeta.Kind = "InfrastructureProvider" // kind gets erased after Create()
			infraProvider.Spec.Version = "v2.0.0"
			Expect(r.reconcileInfrastructureProvider(ctx, infraProvider)).To(Succeed())
			Expect(infraProvider.Spec.Version).To(Equal("v2.0.0"))
		})
	})

	Context("reconcile configmap", func() {
		var cm *corev1.ConfigMap

		BeforeEach(func() {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-api-operator",
					Namespace: controllers.DefaultManagedNamespace,
					Labels:    map[string]string{"foo": "bar"},
				},
				Data: map[string]string{"foo": "bar"},
			}
		})

		AfterEach(func() {
			Expect(test.CleanupAndWait(ctx, cl, cm)).To(Succeed())
		})

		It("should create a configmap", func() {
			Expect(r.reconcileConfigMap(ctx, cm)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      cm.Name,
				Namespace: cm.Namespace,
			}, cm)).To(Succeed())
			Expect(cm.Labels).To(HaveKeyWithValue("foo", "bar"))
			Expect(cm.Data).To(HaveKeyWithValue("foo", "bar"))
		})

		It("should update an existing deployment", func() {
			Expect(cl.Create(ctx, cm)).To(Succeed())
			cm.Labels = map[string]string{"foo": "baz"}
			cm.Data = map[string]string{"foo": "baz"}
			Expect(r.reconcileConfigMap(ctx, cm)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      cm.Name,
				Namespace: cm.Namespace,
			}, cm)).To(Succeed())
			Expect(cm.Labels).To(HaveKeyWithValue("foo", "baz"))
			Expect(cm.Data).To(HaveKeyWithValue("foo", "baz"))
		})
	})
})

var _ = Describe("New image meta", func() {
	It("should parse a full image name", func() {
		imageMeta := newImageMeta("quay.io/foo/bar:baz")
		Expect(imageMeta.Repository).To(Equal("quay.io/foo"))
		Expect(imageMeta.Name).To(Equal("bar"))
		Expect(imageMeta.Tag).To(Equal("baz"))
	})

	It("should parse a full image name with a tag", func() {
		imageMeta := newImageMeta("quay.io/foo/bar:latest")
		Expect(imageMeta.Repository).To(Equal("quay.io/foo"))
		Expect(imageMeta.Name).To(Equal("bar"))
		Expect(imageMeta.Tag).To(Equal("latest"))
	})

	It("should parse a full image name with a digest", func() {
		imageMeta := newImageMeta("quay.io/foo/bar@sha256:baz")
		Expect(imageMeta.Repository).To(Equal("quay.io/foo"))
		Expect(imageMeta.Name).To(Equal("bar@sha256"))
		Expect(imageMeta.Tag).To(Equal("baz"))
	})
})

var _ = Describe("Container customization for provider", func() {
	reconciler := &ClusterOperatorReconciler{
		Images: map[string]string{
			kubeRBACProxyImageName:          kubeRBACProxySource,
			coreProviderImageName:           coreProviderImageSource,
			infrastructureProviderImageName: infrastructureProviderImageSource,
		},
	}

	It("should customize the container for core provider", func() {
		containers := reconciler.containerCustomizationFromProvider(
			"CoreProvider",
			"cluster-api",
			[]operatorv1.ContainerSpec{
				{
					Name: "manager",
				},
			})
		Expect(containers).To(HaveLen(1))
		Expect(containers[0].Name).To(Equal("manager"))
		Expect(containers[0].Image.Name).To(Equal("cluster-api"))
		Expect(containers[0].Image.Repository).To(Equal("test.com"))
		Expect(containers[0].Image.Tag).To(Equal("tag"))
	})
	It("should customize the container for infra provider with proxy", func() {
		containers := reconciler.containerCustomizationFromProvider(
			"InfrastructureProvider",
			"aws",
			[]operatorv1.ContainerSpec{
				{
					Name: "manager",
				},
				{
					Name: "kube-rbac-proxy",
				},
			})

		Expect(containers).To(HaveLen(2))
		Expect(containers[0].Name).To(Equal("manager"))
		Expect(containers[0].Image.Name).To(Equal("cluster-api-provider-aws"))
		Expect(containers[0].Image.Repository).To(Equal("test.com"))
		Expect(containers[0].Image.Tag).To(Equal("tag"))

		Expect(containers[1].Name).To(Equal("kube-rbac-proxy"))
		Expect(containers[1].Image.Name).To(Equal("kube-rbac-proxy"))
		Expect(containers[1].Image.Repository).To(Equal("test.com"))
		Expect(containers[1].Image.Tag).To(Equal("tag"))
	})
})
