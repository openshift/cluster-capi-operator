// Copyright 2026 Red Hat, Inc.
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
package crdcompatibilityoperator

import (
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibilityoperator/bindata"
)

const (
	releaseVersionEnv     = "RELEASE_VERSION"
	defaultReleaseVersion = "0.0.1-snapshot"
)

var (
	//nolint:gochecknoglobals
	staticDeployment = appsv1.Deployment{}
	//nolint:gochecknoglobals
	staticPDB = policyv1.PodDisruptionBudget{}
)

func init() {
	// Parse the embedded deployment YAML on startup
	deploymentYAML, err := bindata.Assets.Asset("assets/deployment.yaml")
	if err != nil {
		panic(fmt.Errorf("failed to read embedded deployment YAML: %w", err))
	}

	if err := yaml.UnmarshalStrict(deploymentYAML, &staticDeployment); err != nil {
		panic(fmt.Errorf("failed to parse embedded deployment YAML: %w", err))
	}

	// Parse the embedded PDB YAML on startup
	pdbYAML, err := bindata.Assets.Asset("assets/pdb.yaml")
	if err != nil {
		panic(fmt.Errorf("failed to read embedded PDB YAML: %w", err))
	}

	if err := yaml.UnmarshalStrict(pdbYAML, &staticPDB); err != nil {
		panic(fmt.Errorf("failed to parse embedded PDB YAML: %w", err))
	}
}

// buildDesiredDeployment constructs the desired Deployment spec by parsing
// the embedded YAML base and overlaying dynamic fields: namespace, replicas,
// container image, and RELEASE_VERSION env var.
func buildDesiredDeployment(namespace, image string, replicas int32) *appsv1.Deployment {
	deployment := staticDeployment.DeepCopy()

	// Overlay dynamic fields
	deployment.Namespace = namespace
	deployment.Spec.Replicas = &replicas
	deployment.Spec.Template.Spec.Containers[0].Image = image

	// Add RELEASE_VERSION env var from the operator's own environment
	releaseVersion := os.Getenv(releaseVersionEnv)
	if releaseVersion == "" {
		releaseVersion = defaultReleaseVersion
	}

	deployment.Spec.Template.Spec.Containers[0].Env = append(
		deployment.Spec.Template.Spec.Containers[0].Env,
		corev1.EnvVar{
			Name:  releaseVersionEnv,
			Value: releaseVersion,
		},
	)

	return deployment
}

// buildDesiredPDB constructs the desired PodDisruptionBudget spec by parsing
// the embedded YAML base and overlaying the namespace.
func buildDesiredPDB(namespace string) *policyv1.PodDisruptionBudget {
	pdb := staticPDB.DeepCopy()
	pdb.Namespace = namespace

	return pdb
}
