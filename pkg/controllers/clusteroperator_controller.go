package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"k8s.io/utils/pointer"
	operatorv1 "sigs.k8s.io/cluster-api/exp/operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/assets"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// ClusterOperatorReconciler reconciles a ClusterOperator object
type ClusterOperatorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	ReleaseVersion   string
	ManagedNamespace string
	Images           map[string]string
	infra            *configv1.Infrastructure
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
	if r.infra == nil {
		infra := &configv1.Infrastructure{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: "cluster"}, infra); err != nil {
			return ctrl.Result{}, err
		}
		r.infra = infra
	}
	featureGate := &configv1.FeatureGate{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: externalFeatureGateName}, featureGate); errors.IsNotFound(err) {
		klog.Infof("FeatureGate cluster does not exist. Skipping...")
		return ctrl.Result{}, r.setStatusAvailable(ctx)
	} else if err != nil {
		klog.Errorf("Unable to retrive FeatureGate object: %v", err)
		return ctrl.Result{}, r.setStatusDegraded(ctx, err)
	}

	// Verify FeatureGate ClusterAPIEnabled is present for operator to work in TP phase
	capiEnabled, err := isCAPIFeatureGateEnabled(featureGate)
	if err != nil {
		klog.Errorf("Could not determine cluster api feature gate state: %v", err)
		return ctrl.Result{}, r.setStatusDegraded(ctx, err)
	}

	var result ctrl.Result
	if capiEnabled {
		klog.Infof("FeatureGate cluster does include cluster api. Installing...")
		result, err = r.reconcile(ctx)
		if err != nil {
			return result, r.setStatusDegraded(ctx, err)
		}
	}

	return result, r.setStatusAvailable(ctx)
}

func (r *ClusterOperatorReconciler) reconcile(ctx context.Context) (ctrl.Result, error) {
	objs, err := assets.FromDir("capi-operator", r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	updater := NewUpdater(objs).WithFilter(func(obj client.Object) bool {
		appliedByManifest := []string{"Namespace", "ClusterRole", "Role", "ClusterRoleBinding", "RoleBinding", "ServiceAccount"}
		// these are already applied by the manifest
		return !util.ContainsString(appliedByManifest, obj.GetObjectKind().GroupVersionKind().Kind)
	})

	err = updater.Mutate(func(obj client.Object) (client.Object, error) {
		dep, depOK := obj.(*appsv1.Deployment)
		if depOK {
			if err := r.customizeDeployment(dep); err != nil {
				return obj, err
			}
		}
		return obj, nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	err = updater.CreateOrUpdate(ctx, r.Client, r.Recorder)
	if err != nil {
		return ctrl.Result{}, err
	}

	objs, err = assets.FromDir("providers", r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	updater = NewUpdater(objs).WithFilter(func(obj client.Object) bool {
		if obj.GetObjectKind().GroupVersionKind().Kind == "InfrastructureProvider" {
			if !r.currentProviderSupportedByCAPI() || !strings.HasPrefix(obj.GetName(), r.currentProviderName()) {
				klog.Infof("skipping infra %s!=%s", obj.GetName(), r.currentProviderName())
				return false
			}
		}
		return true
	})

	err = updater.Mutate(func(obj client.Object) (client.Object, error) {
		infra, ok := obj.(*operatorv1.InfrastructureProvider)
		if ok {
			infra.Spec.ProviderSpec.Deployment = &operatorv1.DeploymentSpec{
				Containers: r.containerCustomizationFromProvider(infra.Kind, infra.Name),
			}
		}
		core, ok := obj.(*operatorv1.CoreProvider)
		if ok {
			core.Spec.ProviderSpec.Deployment = &operatorv1.DeploymentSpec{
				Containers: r.containerCustomizationFromProvider(core.Kind, core.Name),
			}
		}

		return obj, nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	err = updater.CreateOrUpdate(ctx, r.Client, r.Recorder)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, NewUpdater(r.clusterObjects()).CreateOrUpdate(ctx, r.Client, r.Recorder)
}

func providerKindToTypeName(kind string) string {
	return strings.ReplaceAll(strings.ToLower(kind), "provider", "")
}

func (r *ClusterOperatorReconciler) containerCustomizationFromProvider(kind, name string) []operatorv1.ContainerSpec {
	image, ok := r.Images[providerKindToTypeName(kind)+"-"+name+":manager"]
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

func (r *ClusterOperatorReconciler) customizeDeployment(dep *appsv1.Deployment) error {
	containerToImageRef := map[string]string{
		"manager":         "cluster-api:operator",
		"kube-rbac-proxy": "kube-rbac-proxy",
	}
	for ci, cont := range dep.Spec.Template.Spec.Containers {
		if cont.Name == "manager" {
			// since our RBAC is installed via /manifests we don't want the upstream operator
			// to delete them as it normally would on upgrade.
			dep.Spec.Template.Spec.Containers[ci].Args = append(cont.Args, "--delete-rbac-on-upgrade=false")
		}

		if imageRef, ok := containerToImageRef[cont.Name]; ok {
			if cont.Image == r.Images[imageRef] {
				klog.Infof("container %s image %s", cont.Name, cont.Image)
				continue
			}
			klog.Infof("container %s changing image from %s to %s", cont.Name, cont.Image, r.Images[imageRef])
			dep.Spec.Template.Spec.Containers[ci].Image = r.Images[imageRef]
		} else {
			klog.Warningf("container %s no image replacement found for %s", cont.Name, cont.Image)
		}
	}
	return setSpecHashAnnotation(&dep.ObjectMeta, dep.Spec)
}

func setSpecHashAnnotation(objMeta *metav1.ObjectMeta, spec interface{}) error {
	jsonBytes, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	specHash := fmt.Sprintf("%x", sha256.Sum256(jsonBytes))
	if objMeta.Annotations == nil {
		objMeta.Annotations = map[string]string{}
	}
	objMeta.Annotations[specHashAnnotation] = specHash
	return nil
}
