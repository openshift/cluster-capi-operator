package framework

import (
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetControlPlaneHostAndPort(cl client.Client) (string, int32, error) {
	var infraCluster configv1.Infrastructure
	namespacedName := client.ObjectKey{
		Namespace: "openshift-cluster-api",
		Name:      "cluster",
	}

	if err := cl.Get(ctx, namespacedName, &infraCluster); err != nil {
		return "", 0, err
	}

	hostAndPort := strings.TrimPrefix(infraCluster.Status.APIServerURL, "https://")
	apiEndpoint := strings.Split(hostAndPort, ":")
	if len(apiEndpoint) < 2 {
		return apiEndpoint[0], 0, nil
	}

	port, err := strconv.ParseInt(apiEndpoint[1], 10, 32)
	if err != nil {
		return apiEndpoint[0], 0, nil
	}

	return apiEndpoint[0], int32(port), nil
}
