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

	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetNodeForMachine retrieves the node backing the given Machine.
func GetNodeForMachine(ctx context.Context, cl client.Client, m *clusterv1.Machine) (*corev1.Node, error) {
	if m.Status.NodeRef == nil {
		return nil, fmt.Errorf("%s: machine has no NodeRef", m.Name)
	}

	node := &corev1.Node{}
	nodeName := client.ObjectKey{Name: m.Status.NodeRef.Name}

	if err := cl.Get(ctx, nodeName, node); err != nil {
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	return node, nil
}

// isNodeReady returns true if the given node is ready.
func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}
