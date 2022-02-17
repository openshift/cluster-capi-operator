package controllers

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"k8s.io/utils/pointer"
	operatorv1 "sigs.k8s.io/cluster-api/exp/operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/assets"
)

// ClusterOperatorReconciler reconciles a ClusterOperator object
type ClusterOperatorReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	Recorder           record.EventRecorder
	ReleaseVersion     string
	ManagedNamespace   string
	Images             map[string]string
	PlatformType       string
	SupportedPlatforms map[string]bool
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Watches(
			&source.Kind{Type: &configv1.Infrastructure{}},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(infrastructurePredicates()),
		).
		Watches(
			&source.Kind{Type: &configv1.FeatureGate{}},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(featureGatePredicates()),
		).
		Complete(r)
}

// Reconcile will process the cluster-api clusterOperator
func (r *ClusterOperatorReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	klog.Infof("Intalling Cluster API components for technical preview cluster")
	// Get infrastructure object
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: infrastructureResourceName}, infra); errors.IsNotFound(err) {
		klog.Infof("Infrastructure cluster does not exist. Skipping...")
		if err := r.setStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		klog.Errorf("Unable to retrive Infrastructure object: %v", err)
		if err := r.setStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Install upstream CAPI Operator
	if err := r.installCAPIOperator(ctx); err != nil {
		klog.Errorf("Unable to install CAPI operator: %v", err)
		if err := r.setStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Install core CAPI components
	if err := r.installCoreCAPIComponents(ctx); err != nil {
		klog.Errorf("Unable to install core CAPI components: %v", err)
		if err := r.setStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Set platform type
	if infra.Status.PlatformStatus == nil {
		klog.Infof("No platform status exists in infrastructure object. Skipping...")
		if err := r.setStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	r.PlatformType = strings.ToLower(string(infra.Status.PlatformStatus.Type))

	// Check if platform type is supported
	if _, ok := r.SupportedPlatforms[r.PlatformType]; !ok {
		klog.Infof("Platform type %s is not supported. Skipping...", r.PlatformType)
		if err := r.setStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Install infrastructure CAPI components
	if err := r.installInfrastructureCAPIComponents(ctx); err != nil {
		klog.Errorf("Unable to infrastructure core CAPI components: %v", err)
		if err := r.setStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.setStatusAvailable(ctx)
}

// installCAPIOperator reads assets from assets/capi-operator, customizes Deployment and Service objects, and applies them
func (r *ClusterOperatorReconciler) installCAPIOperator(ctx context.Context) error {
	klog.Infof("Installing CAPI Operator")
	objs, err := assets.FromDir("capi-operator", r.Scheme)
	if err != nil {
		return fmt.Errorf("unable to read capi-operator assets: %v", err)
	}

	for _, obj := range objs {
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "Deployment":
			deployment := obj.(*appsv1.Deployment)
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
				containerToImageRef := map[string]string{
					"manager":         "cluster-api:operator",
					"kube-rbac-proxy": "kube-rbac-proxy",
				}
				for ci, cont := range deployment.Spec.Template.Spec.Containers {
					if imageRef, ok := containerToImageRef[cont.Name]; ok {
						if cont.Image == r.Images[imageRef] {
							klog.Infof("container %s image %s", cont.Name, cont.Image)
							continue
						}
						klog.Infof("container %s changing image from %s to %s", cont.Name, cont.Image, r.Images[imageRef])
						deployment.Spec.Template.Spec.Containers[ci].Image = r.Images[imageRef]
					} else {
						klog.Warningf("container %s no image replacement found for %s", cont.Name, cont.Image)
					}
				}
				return nil
			}); err != nil {
				return fmt.Errorf("unable to create or update upstream CAPI operator Deployment: %v", err)
			}
		default:
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
				return nil
			}); err != nil {
				return fmt.Errorf("unable to create or update upstream CAPI operator %s: %v", obj.GetObjectKind().GroupVersionKind().Kind, err)
			}
		}
	}
	return nil
}

// installCoreCAPIComponents reads assets from assets/core-capi, create CRs that are consumed by upstream CAPI Operator
func (r *ClusterOperatorReconciler) installCoreCAPIComponents(ctx context.Context) error {
	klog.Infof("Installing Core CAPI components")
	objs, err := assets.FromDir("core-capi", r.Scheme)
	if err != nil {
		return fmt.Errorf("unable to read core-capi: %v", err)
	}

	for _, obj := range objs {
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "CoreProvider":
			coreProvider := obj.(*operatorv1.CoreProvider)
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, coreProvider, func() error {
				coreProvider.Spec.ProviderSpec.Deployment = &operatorv1.DeploymentSpec{
					Containers: r.containerCustomizationFromProvider(coreProvider.Kind, coreProvider.Name),
				}
				return nil
			}); err != nil {
				return fmt.Errorf("unable to create or update CoreProvider: %v", err)
			}
		default:
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
				return nil
			}); err != nil {
				return fmt.Errorf("unable to create or update core Cluster API Configmap: %v", err)
			}
		}
	}
	return nil
}

// installInfrastructureCAPIComponents reads assets from assets/providers, create CRs that are consumed by upstream CAPI Operator
func (r *ClusterOperatorReconciler) installInfrastructureCAPIComponents(ctx context.Context) error {
	klog.Infof("Installing Infrastructure CAPI components")
	objs, err := assets.FromDir("infrastructure-providers", r.Scheme)
	if err != nil {
		return fmt.Errorf("unable to read providers: %v", err)
	}

	for _, obj := range objs {
		// provider assets name always match with platform name, example: Name: aws
		if !strings.HasPrefix(obj.GetName(), r.PlatformType) {
			continue
		}
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "InfrastructureProvider":
			infraProvider := obj.(*operatorv1.InfrastructureProvider)
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, infraProvider, func() error {
				infraProvider.Spec.ProviderSpec.Deployment = &operatorv1.DeploymentSpec{
					Containers: r.containerCustomizationFromProvider(infraProvider.Kind, infraProvider.Name),
				}
				return nil
			}); err != nil {
				return fmt.Errorf("unable to create or update InfrastructureProvider: %v", err)
			}
		default:
			if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
				return nil
			}); err != nil {
				return fmt.Errorf("unable to create or update infastructure Cluster API Configmap: %v", err)
			}
		}
	}

	return nil
}

// containerCustomizationFromProvider returns a list of containers customized for the given provider
func (r *ClusterOperatorReconciler) containerCustomizationFromProvider(kind, name string) []operatorv1.ContainerSpec {
	image, ok := r.Images[providerKindToTypeName(kind)+"-"+name+":manager"] // example: infrastructure-aws:manager
	cSpecs := []operatorv1.ContainerSpec{}
	if !ok {
		return cSpecs
	}
	cSpecs = append(cSpecs, operatorv1.ContainerSpec{
		Name:  "manager",
		Image: newImageMeta(image),
	})
	if kind == "InfrastructureProvider" {
		image, ok := r.Images["kube-rbac-proxy"]
		if !ok {
			return cSpecs
		}
		cSpecs = append(cSpecs, operatorv1.ContainerSpec{
			Name:  "kube-rbac-proxy",
			Image: newImageMeta(image),
		})
	}

	return cSpecs
}

func providerKindToTypeName(kind string) string {
	return strings.ReplaceAll(strings.ToLower(kind), "provider", "")
}

func newImageMeta(imageURL string) *operatorv1.ImageMeta {
	im := &operatorv1.ImageMeta{}
	urlSplit := strings.Split(imageURL, ":")
	if len(urlSplit) == 2 {
		im.Tag = &urlSplit[1]
	}
	urlSplit = strings.Split(urlSplit[0], "/")
	im.Name = &urlSplit[len(urlSplit)-1]
	im.Repository = pointer.StringPtr(strings.Join(urlSplit[0:len(urlSplit)-1], "/"))
	return im
}
