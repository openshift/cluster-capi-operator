package kubeconfig

import (
	"errors"

	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

type kubeconfigOptions struct {
	token            []byte
	caCert           []byte
	apiServerEnpoint string
	clusterName      string
}

func generateKubeconfig(options kubeconfigOptions) (*api.Config, error) {
	if len(options.token) == 0 {
		return nil, errors.New("token can't be empty")
	}

	if len(options.caCert) == 0 {
		return nil, errors.New("ca cert can't be empty")
	}

	if options.apiServerEnpoint == "" {
		return nil, errors.New("api server endpoint can't be empty")
	}

	if options.clusterName == "" {
		return nil, errors.New("cluster name can't be empty")
	}

	userName := "cluster-capi-operator"
	kubeconfig := &api.Config{
		Clusters: map[string]*api.Cluster{
			options.clusterName: {
				Server:                   options.apiServerEnpoint,
				CertificateAuthorityData: options.caCert,
			},
		},
		Contexts: map[string]*api.Context{
			options.clusterName: {
				Cluster:   options.clusterName,
				AuthInfo:  userName,
				Namespace: controllers.DefaultManagedNamespace,
			},
		},
		AuthInfos: map[string]*api.AuthInfo{
			userName: {
				Token: string(options.token),
			},
		},
		CurrentContext: options.clusterName,
	}

	return kubeconfig, nil
}
