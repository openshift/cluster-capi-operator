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

package machinesync

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Unit tests for fetchCAPIInfraResources because reconciling deletion depends on it.
var _ = Describe("Unit tests for fetchCAPIInfraResources", func() {
	var reconciler *MachineSyncReconciler
	var capiMachine *clusterv1.Machine

	BeforeEach(func() {
		capiMachine = &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "test-namespace",
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: "test-cluster",
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: awsv1.GroupVersion.Group,
					Kind:     "AWSMachine",
					Name:     "test-machine",
				},
			},
		}

		reconciler = &MachineSyncReconciler{
			Scheme:   testEnv.Scheme,
			Platform: configv1.AWSPlatformType,
		}
	})

	Describe("when fetching Cluster API Infrastructure Resources", func() {
		Context("when capiMachine is nil", func() {
			It("should return nil and no error", func() {
				reconciler.Client = fake.NewClientBuilder().WithScheme(testEnv.Scheme).Build()
				infraCluster, infraMachine, err := reconciler.fetchCAPIInfraResources(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(infraCluster).To(BeNil())
				Expect(infraMachine).To(BeNil())
			})
		})

		Context("and Infrastructure Cluster is not present", func() {
			It("should return nil for both infra cluster and infra machine", func() {
				reconciler.Client = fake.NewClientBuilder().WithScheme(testEnv.Scheme).Build()

				infraCluster, infraMachine, err := reconciler.fetchCAPIInfraResources(ctx, capiMachine)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("failed to get Cluster API infrastructure cluster")))
				Expect(infraCluster).To(BeNil())
				Expect(infraMachine).To(BeNil())
			})
		})

		Context("and Infrastructure is present", func() {
			var objs []client.Object
			BeforeEach(func() {
				objs = []client.Object{
					&awsv1.AWSCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      capiMachine.Spec.ClusterName,
							Namespace: capiMachine.Namespace,
						},
					},
				}
			})

			It("should return nil if infrastructure machine is not present", func() {
				reconciler.Client = fake.NewClientBuilder().WithScheme(testEnv.Scheme).WithObjects(objs...).Build()
				infraCluster, infraMachine, err := reconciler.fetchCAPIInfraResources(ctx, capiMachine)

				Expect(err).ToNot(HaveOccurred())
				Expect(infraCluster).ToNot(BeNil())
				Expect(infraMachine).To(BeNil())
			})

			It("should return infrastructure machine if it is present", func() {
				objs = append(objs, &awsv1.AWSMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      capiMachine.Name,
						Namespace: capiMachine.Namespace,
					},
				})
				reconciler.Client = fake.NewClientBuilder().WithScheme(testEnv.Scheme).WithObjects(objs...).Build()
				infraCluster, infraMachine, err := reconciler.fetchCAPIInfraResources(ctx, capiMachine)

				Expect(err).ToNot(HaveOccurred())
				Expect(infraCluster).ToNot(BeNil())
				Expect(infraMachine).ToNot(BeNil())
			})
		})
	})
})
