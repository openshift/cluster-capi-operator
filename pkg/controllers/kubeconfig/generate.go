/*
Copyright 2024 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package kubeconfig

import (
	"errors"

	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

var (
	errTokenEmpty             = errors.New("token can't be empty")
	errCACertEmpty            = errors.New("ca cert can't be empty")
	errAPIServerEndpointEmpty = errors.New("api server endpoint can't be empty")
	errClusterNameEmpty       = errors.New("cluster name can't be empty")
)

type kubeconfigOptions struct {
	token            []byte
	caCert           []byte
	apiServerEnpoint string
	clusterName      string
}

func generateKubeconfig(options kubeconfigOptions) (*api.Config, error) {
	if len(options.token) == 0 {
		return nil, errTokenEmpty
	}

	if len(options.caCert) == 0 {
		return nil, errCACertEmpty
	}

	if options.apiServerEnpoint == "" {
		return nil, errAPIServerEndpointEmpty
	}

	if options.clusterName == "" {
		return nil, errClusterNameEmpty
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
