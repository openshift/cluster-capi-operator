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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Unit tests for ensureCAPIInfraMachineDeleted", func() {
	var (
		reconciler    *MachineSyncReconciler
		mapiMachine   *mapiv1beta1.Machine
		awsMachine    *awsv1.AWSMachine
		notFoundError *apierrors.StatusError
	)

	BeforeEach(func() {
		mapiMachine = &mapiv1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "openshift-machine-api",
			},
		}

		awsMachine = &awsv1.AWSMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-machine",
				Namespace:  "openshift-cluster-api",
				Finalizers: []string{"awsmachine.infrastructure.cluster.x-k8s.io"},
			},
		}

		notFoundError = apierrors.NewNotFound(
			schema.GroupResource{Group: "infrastructure.cluster.x-k8s.io", Resource: "awsmachines"},
			"test-machine",
		)

		reconciler = &MachineSyncReconciler{
			Scheme:   testEnv.Scheme,
			Platform: configv1.AWSPlatformType,
		}
	})

	Context("when delete and update both succeed", func() {
		It("should return progressing true and no error", func() {
			// Use an AWSMachine without finalizers so that Delete removes
			// it immediately and the subsequent Update returns NotFound,
			// which is the tolerated success path.
			awsMachineNoFinalizers := awsMachine.DeepCopy()
			awsMachineNoFinalizers.Finalizers = nil

			reconciler.Client = fake.NewClientBuilder().
				WithScheme(testEnv.Scheme).
				WithObjects(mapiMachine, awsMachineNoFinalizers).
				Build()

			progressing, err := reconciler.ensureCAPIInfraMachineDeleted(ctx, mapiMachine, awsMachineNoFinalizers)
			Expect(err).ToNot(HaveOccurred())
			Expect(progressing).To(BeTrue())
		})
	})

	Context("when the infrastructure machine is already deleted", func() {
		It("should treat delete NotFound as success", func() {
			reconciler.Client = fake.NewClientBuilder().
				WithScheme(testEnv.Scheme).
				WithObjects(mapiMachine).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
						return notFoundError
					},
				}).
				Build()

			progressing, err := reconciler.ensureCAPIInfraMachineDeleted(ctx, mapiMachine, awsMachine)
			Expect(err).ToNot(HaveOccurred())
			Expect(progressing).To(BeTrue())
		})
	})

	Context("when the infrastructure machine is garbage collected between delete and update", func() {
		It("should treat update NotFound as success", func() {
			reconciler.Client = fake.NewClientBuilder().
				WithScheme(testEnv.Scheme).
				WithObjects(mapiMachine, awsMachine).
				WithInterceptorFuncs(interceptor.Funcs{
					Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
						return notFoundError
					},
				}).
				Build()

			progressing, err := reconciler.ensureCAPIInfraMachineDeleted(ctx, mapiMachine, awsMachine)
			Expect(err).ToNot(HaveOccurred())
			Expect(progressing).To(BeTrue())
		})
	})

	Context("when delete fails with a non-NotFound error", func() {
		It("should return the error", func() {
			reconciler.Client = fake.NewClientBuilder().
				WithScheme(testEnv.Scheme).
				WithObjects(mapiMachine).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
						return fmt.Errorf("connection refused")
					},
				}).
				Build()

			progressing, err := reconciler.ensureCAPIInfraMachineDeleted(ctx, mapiMachine, awsMachine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to delete Cluster API Infrastructure machine"))
			Expect(progressing).To(BeFalse())
		})
	})

	Context("when update fails with a non-NotFound error", func() {
		It("should return the error", func() {
			reconciler.Client = fake.NewClientBuilder().
				WithScheme(testEnv.Scheme).
				WithObjects(mapiMachine, awsMachine).
				WithInterceptorFuncs(interceptor.Funcs{
					Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
						return fmt.Errorf("connection refused")
					},
				}).
				Build()

			progressing, err := reconciler.ensureCAPIInfraMachineDeleted(ctx, mapiMachine, awsMachine)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to remove finalizer for deleting Cluster API Infrastructure machine"))
			Expect(progressing).To(BeFalse())
		})
	})
})
