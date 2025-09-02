// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"

	. "github.com/onsi/gomega"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	bmov1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	infrastructureName                                                = "cluster"
	infraAPIVersion                                                   = "infrastructure.cluster.x-k8s.io/v1beta1"
	managedByAnnotationValueClusterCAPIOperatorInfraClusterController = "cluster-capi-operator-infracluster-controller"
)

var (
	cl          client.Client
	ctx         = context.Background()
	platform    configv1.PlatformType
	clusterName string
)

func init() {
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(gcpv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(azurev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(mapiv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(ibmpowervsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(vspherev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(metal3v1.AddToScheme(scheme.Scheme))
	utilruntime.Must(bmov1alpha1.AddToScheme(scheme.Scheme))
}

// InitCommonVariables initializes global variables used across test cases.
func InitCommonVariables() {
	cfg, err := config.GetConfig()
	Expect(err).ToNot(HaveOccurred())

	cl, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())

	infra := &configv1.Infrastructure{}
	infraName := client.ObjectKey{
		Name: infrastructureName,
	}
	Expect(cl.Get(ctx, infraName, infra)).To(Succeed())
	Expect(infra.Status.PlatformStatus).ToNot(BeNil())
	clusterName = infra.Status.InfrastructureName
	platform = infra.Status.PlatformStatus.Type

	komega.SetClient(cl)
	komega.SetContext(ctx)
}
