/*
Copyright 2025 Red Hat, Inc.

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
package util

import (
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

// TestNormalizeOwnerReferences tests the normalizeOwnerReferences function.
// TODO: Remove this once we have migrated to v1beta2.
//
//nolint:funlen
func TestNormalizeOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	testUID := types.UID("test-uid-12345")

	tests := []struct {
		name     string
		input    []metav1.OwnerReference
		expected []metav1.OwnerReference
	}{
		{
			name:     "empty slice",
			input:    []metav1.OwnerReference{},
			expected: []metav1.OwnerReference{},
		},
		{
			name: "v1beta1 remains v1beta1",
			input: []metav1.OwnerReference{
				{
					APIVersion:         "cluster.x-k8s.io/v1beta1",
					Kind:               "MachineSet",
					Name:               "test-machineset",
					UID:                testUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion:         "cluster.x-k8s.io/v1beta1",
					Kind:               "MachineSet",
					Name:               "test-machineset",
					UID:                testUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		{
			name: "v1beta2 normalized to v1beta1",
			input: []metav1.OwnerReference{
				{
					APIVersion:         "cluster.x-k8s.io/v1beta2",
					Kind:               "MachineSet",
					Name:               "test-machineset",
					UID:                testUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion:         "cluster.x-k8s.io/v1beta1",
					Kind:               "MachineSet",
					Name:               "test-machineset",
					UID:                testUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		{
			name: "non-CAPI group unchanged",
			input: []metav1.OwnerReference{
				{
					APIVersion:         "machine.openshift.io/v1beta1",
					Kind:               "MachineSet",
					Name:               "test-machineset",
					UID:                testUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion:         "machine.openshift.io/v1beta1",
					Kind:               "MachineSet",
					Name:               "test-machineset",
					UID:                testUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		{
			name: "multiple refs with mixed versions",
			input: []metav1.OwnerReference{
				{
					APIVersion: "cluster.x-k8s.io/v1beta2",
					Kind:       "MachineSet",
					Name:       "test-machineset",
					UID:        testUID,
				},
				{
					APIVersion: "cluster.x-k8s.io/v1beta1",
					Kind:       "Cluster",
					Name:       "test-cluster",
					UID:        types.UID("cluster-uid"),
				},
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "test-deployment",
					UID:        types.UID("deployment-uid"),
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion: "cluster.x-k8s.io/v1beta1",
					Kind:       "MachineSet",
					Name:       "test-machineset",
					UID:        testUID,
				},
				{
					APIVersion: "cluster.x-k8s.io/v1beta1",
					Kind:       "Cluster",
					Name:       "test-cluster",
					UID:        types.UID("cluster-uid"),
				},
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "test-deployment",
					UID:        types.UID("deployment-uid"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeOwnerReferences(tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

// TestObjectMetaEqual_OwnerReferences tests the ObjectMetaEqual function for owner references.
// TODO: Update this once we have migrated to v1beta2.
//
//nolint:funlen
func TestObjectMetaEqual_OwnerReferences(t *testing.T) {
	g := NewWithT(t)

	testUID := types.UID("test-uid-12345")

	tests := []struct {
		name           string
		metaA          metav1.ObjectMeta
		metaB          metav1.ObjectMeta
		expectNoDiff   bool
		diffKeyPresent string
	}{
		{
			name: "identical owner references (v1beta1)",
			metaA: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "cluster.x-k8s.io/v1beta1",
						Kind:               "MachineSet",
						Name:               "test-machineset",
						UID:                testUID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					},
				},
			},
			metaB: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "cluster.x-k8s.io/v1beta1",
						Kind:               "MachineSet",
						Name:               "test-machineset",
						UID:                testUID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					},
				},
			},
			expectNoDiff: true,
		},
		{
			name: "v1beta1 vs v1beta2 should be treated as equal",
			metaA: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "cluster.x-k8s.io/v1beta2",
						Kind:               "MachineSet",
						Name:               "test-machineset",
						UID:                testUID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					},
				},
			},
			metaB: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "cluster.x-k8s.io/v1beta1",
						Kind:               "MachineSet",
						Name:               "test-machineset",
						UID:                testUID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					},
				},
			},
			expectNoDiff: true,
		},
		{
			name: "different name should show diff",
			metaA: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "cluster.x-k8s.io/v1beta1",
						Kind:       "MachineSet",
						Name:       "test-machineset-a",
						UID:        testUID,
					},
				},
			},
			metaB: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "cluster.x-k8s.io/v1beta1",
						Kind:       "MachineSet",
						Name:       "test-machineset-b",
						UID:        testUID,
					},
				},
			},
			expectNoDiff:   false,
			diffKeyPresent: ".ownerReferences",
		},
		{
			name: "different kind should show diff",
			metaA: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "cluster.x-k8s.io/v1beta1",
						Kind:       "MachineSet",
						Name:       "test",
						UID:        testUID,
					},
				},
			},
			metaB: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "cluster.x-k8s.io/v1beta1",
						Kind:       "Cluster",
						Name:       "test",
						UID:        testUID,
					},
				},
			},
			expectNoDiff:   false,
			diffKeyPresent: ".ownerReferences",
		},
		{
			name: "non-CAPI version difference should show diff",
			metaA: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test",
						UID:        testUID,
					},
				},
			},
			metaB: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1beta1",
						Kind:       "Deployment",
						Name:       "test",
						UID:        testUID,
					},
				},
			},
			expectNoDiff:   false,
			diffKeyPresent: ".ownerReferences",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := ObjectMetaEqual(tt.metaA, tt.metaB)
			if tt.expectNoDiff {
				g.Expect(diff).To(BeEmpty(), "Expected no diff but got: %v", diff)
			} else {
				g.Expect(diff).NotTo(BeEmpty(), "Expected diff but got none")

				if tt.diffKeyPresent != "" {
					g.Expect(diff).To(HaveKey(tt.diffKeyPresent))
				}
			}
		})
	}
}
