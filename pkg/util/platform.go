package util

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	infrastructureResourceName = "cluster"
)

func GetPlatform(ctx context.Context, cl client.Reader) (configv1.PlatformType, error) {
	infra := &configv1.Infrastructure{}

	if err := cl.Get(ctx, client.ObjectKey{Name: infrastructureResourceName}, infra); err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}

	if infra.Status.PlatformStatus == nil {
		return "", nil
	}

	return infra.Status.PlatformStatus.Type, nil
}
