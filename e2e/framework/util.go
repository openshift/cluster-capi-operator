package framework

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetControlPlaneHostAndPort(cl client.Client) (string, int32, error) {
	var infraCluster configv1.Infrastructure
	namespacedName := client.ObjectKey{
		Namespace: CAPINamespace,
		Name:      "cluster",
	}

	if err := cl.Get(ctx, namespacedName, &infraCluster); err != nil {
		return "", 0, fmt.Errorf("failed to get the infrastructure object: %w", err)
	}

	apiUrl, err := url.Parse(infraCluster.Status.APIServerURL)
	if err != nil {
		return "", 0, fmt.Errorf("failed to parse the API server URL: %w", err)
	}

	port, err := strconv.ParseInt(apiUrl.Port(), 10, 32)
	if err != nil {
		return apiUrl.Hostname(), 0, fmt.Errorf("failed to parse port: %w", err)
	}

	return apiUrl.Hostname(), int32(port), nil
}

// IsMachineAPIMigrationEnabled checks if the "MachineAPIMigration" feature is enabled via FeatureGate status
func IsMachineAPIMigrationEnabled(ctx context.Context, cl client.Client) bool {
	// Get the cluster's desired version
	clusterVersion := &configv1.ClusterVersion{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "version"}, clusterVersion); err != nil {
		return false
	}

	// Get the current desired version
	var desiredVersion string
	for _, history := range clusterVersion.Status.History {
		if history.State == "Completed" {
			desiredVersion = history.Version
			break
		}
	}
	if desiredVersion == "" {
		return false
	}

	featureGate := &configv1.FeatureGate{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "cluster"}, featureGate); err != nil {
		return false
	}

	for _, fg := range featureGate.Status.FeatureGates {
		if fg.Version != desiredVersion {
			continue // Skip versions that don't match our desired version
		}
		for _, enabled := range fg.Enabled {
			if enabled.Name == "MachineAPIMigration" {
				return true
			}
		}
	}

	return false
}
