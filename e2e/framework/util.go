package framework

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
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
	// First we need to check ClusterVersion because:
	// 1. Feature gates might change across versions
	// 2. For upgrade, we need to select only the version that we upgraded to
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}
	Eventually(komega.Get(clusterVersion)).Should(Succeed(), "clusterVersion should be available")

	desiredVersion := clusterVersion.Status.Desired.Version
	if len(desiredVersion) == 0 && len(clusterVersion.Status.History) > 0 {
		desiredVersion = clusterVersion.Status.History[0].Version
	}

	featureGate := &configv1.FeatureGate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	Eventually(komega.Get(featureGate)).Should(Succeed(), "featureGate should be available")

	for _, fg := range featureGate.Status.FeatureGates {
		if fg.Version != desiredVersion {
			continue
		}
		for _, enabled := range fg.Enabled {
			if enabled.Name == "MachineAPIMigration" {
				return true
			}
		}
	}

	return false
}
