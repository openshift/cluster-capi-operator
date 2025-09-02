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

package framework

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateCoreCluster creates a cluster with the given name and returns the cluster object.
func CreateCoreCluster(cl client.Client, clusterName, infraClusterKind string) *clusterv1.Cluster {
	By("Creating core cluster")

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: CAPINamespace,
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
				Kind:       infraClusterKind,
				Name:       clusterName,
				Namespace:  CAPINamespace,
			},
		},
	}
	// TODO(damdo): is there a way to avoid doing this in the generic framework?
	if infraClusterKind == "VSphereCluster" {
		host, port, err := GetControlPlaneHostAndPort(cl)
		if err != nil {
			Expect(err).ToNot(HaveOccurred())
		}

		cluster.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
			Host: host,
			Port: port,
		}
	}

	if err := cl.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}

	Eventually(func() (bool, error) {
		patchedCluster := &clusterv1.Cluster{}
		err := cl.Get(ctx, client.ObjectKeyFromObject(cluster), patchedCluster)
		if err != nil {
			return false, fmt.Errorf("failed to get cluster: %w", err)
		}

		return conditions.IsTrue(patchedCluster, clusterv1.ControlPlaneInitializedCondition), nil
	}, WaitMedium).Should(BeTrue())

	return cluster
}
