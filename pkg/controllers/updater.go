package controllers

import (
	"context"

	"k8s.io/client-go/tools/record"
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
	for i := range u.objs {
		if !objectFilterFn(u.objs[i]) {
			u.objs = append(u.objs[:i], u.objs[i+1:]...)
		}
	}
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
		existing := u.objs[i].DeepCopyObject().(client.Object)
		opRes, err := ctrl.CreateOrUpdate(ctx, c, existing, func() error {
			existing = u.objs[i].(client.Object)
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
