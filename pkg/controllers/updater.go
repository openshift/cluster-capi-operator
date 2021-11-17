package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type updater struct {
	objs []client.Object
}

type ObjectMutateFn func(client.Object) (client.Object, error)
type ObjectFilterFn func(client.Object) bool

type Updater interface {
	// WithFilter with call ObjectFilterFn and only keep object that the function returns true.
	WithFilter(objectFilterFn ObjectFilterFn) Updater
	// Mutate will call the ObjectMutateFn for each object.
	Mutate(objectMutateFn ObjectMutateFn) error
	// CreateOrUpdate will create or update all objects.
	CreateOrUpdate(ctx context.Context, c client.Client, r record.EventRecorder) error
}

func NewUpdater(objs []client.Object) Updater {
	return &updater{objs: objs}
}

func (u *updater) WithFilter(objectFilterFn ObjectFilterFn) Updater {
	filteredObjs := []client.Object{}
	for _, obj := range u.objs {
		if objectFilterFn(obj) {
			filteredObjs = append(filteredObjs, obj)
		}
	}
	u.objs = filteredObjs
	return u
}

func (u *updater) Mutate(objectMutateFn ObjectMutateFn) error {
	for i := range u.objs {
		o, err := objectMutateFn(u.objs[i])
		if err != nil {
			return err
		}
		u.objs[i] = o
	}
	return nil
}

func (u *updater) CreateOrUpdate(ctx context.Context, c client.Client, r record.EventRecorder) error {
	for i := range u.objs {
		required, err := toUnstructured(u.objs[i])
		if err != nil {
			return err
		}
		existing, err := toUnstructured(u.objs[i].DeepCopyObject())
		if err != nil {
			return err
		}

		klog.Infof("createOrUpdating %s %s", existing.GetKind(), existing.GetName())
		opRes, err := ctrl.CreateOrUpdate(ctx, c, existing, func() error {
			rv := existing.GetResourceVersion()
			required.DeepCopyInto(existing)
			existing.SetResourceVersion(rv)

			return nil
		})
		if err != nil {
			return err
		}

		if err == nil {
			r.Eventf(existing, "Normal", string(opRes), "success")
		} else {
			r.Eventf(existing, "Warning", "CreateOrUpdateFailed", "Failed to CreateOrUpdate:%v", err)
		}
	}

	return nil
}

func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	// If the incoming object is already unstructured, perform a deep copy first
	// otherwise DefaultUnstructuredConverter ends up returning the inner map without
	// making a copy.
	if _, ok := obj.(runtime.Unstructured); ok {
		obj = obj.DeepCopyObject()
	}
	rawMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: rawMap}, nil
}
