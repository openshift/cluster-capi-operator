package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	configclient "sigs.k8s.io/cluster-api/cmd/clusterctl/client/config"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/repository"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
	"sigs.k8s.io/yaml"
)

var (
	// capiOperatorManifests is a list of CAPI Operator manifests
	// TODO: remove when have upstream repo set up
	capiOperatorManifests = path.Join(projDir, "hack", "import-assets", "operator-components.yaml")
	operatorComponentsUrl = "https://raw.githubusercontent.com/openshift/cluster-api-operator/main/openshift/operator-components.yaml"
)

func importCAPIOperator() error {
	fmt.Println("Processing Cluster API Operator")

	// Load CAPI Operator manifests from file
	objs, err := readCAPIOperatorManifests()
	if err != nil {
		return fmt.Errorf("failed to read CAPI Operator manifests: %v", err)
	}

	// Change cert-manager annotations to service-ca, because openshift doesn't support cert-manager
	objs = certManagerToServiceCA(objs)

	// Mutate resources
	objs = mutateOperatorResources(objs)

	// Split out RBAC, CRDs and Service objects
	resourceMap := splitResources(objs)

	// Write RBAC components to manifests, they will be managed by CVO
	rbacFileName := fmt.Sprintf("%s%s_03_rbac-roles.%s.yaml", manifestPrefix, "capi-operator", "upstream")
	err = writeComponentsToManifests(rbacFileName, resourceMap[rbacKey])
	if err != nil {
		return err
	}

	// Write CRD components to manifests, they will be managed by CVO
	crdFileName := fmt.Sprintf("%s%s_02_crd.%s.yaml", manifestPrefix, "capi-operator", "upstream")
	err = writeComponentsToManifests(crdFileName, resourceMap[crdKey])
	if err != nil {
		return err
	}

	// Write all other components(deployments, services, secret, etc)
	return writeAllOtherOperatorComponents(resourceMap[otherKey])
}

func readCAPIOperatorManifests() ([]unstructured.Unstructured, error) {
	// Create new clusterctl config client
	configClient, err := configclient.New("")
	if err != nil {
		return nil, err
	}

	// Create new clusterctl provider client
	// The arguments to Get() do nothing in this case, but they are required for setting up a yaml processor
	providerConfig, err := configClient.Providers().Get("cluster-api", "CoreProvider")
	if err != nil {
		return nil, err
	}

	// Download provider components from github as raw yaml
	componentsResponse, err := http.Get(operatorComponentsUrl)
	if err != nil {
		return nil, err
	}
	defer componentsResponse.Body.Close()

	rawComponentsResponse, err := io.ReadAll(componentsResponse.Body)
	if err != nil {
		return nil, err
	}

	// Set options for yaml processor
	options := repository.ComponentsOptions{
		TargetNamespace:     targetNamespace,
		SkipTemplateProcess: false,
	}

	// Process operator manifests
	components, err := repository.NewComponents(repository.ComponentsInput{
		Provider:     providerConfig,
		ConfigClient: configClient,
		Processor:    yamlprocessor.NewSimpleProcessor(),
		RawYaml:      rawComponentsResponse,
		Options:      options,
	})
	if err != nil {
		return nil, err
	}

	return components.Objs(), nil
}

func mutateOperatorResources(objs []unstructured.Unstructured) []unstructured.Unstructured {
	finalObjs := []unstructured.Unstructured{}
	for _, obj := range objs {
		switch obj.GetKind() {
		case "Deployment":
			deployment := &appsv1.Deployment{}
			if err := scheme.Convert(&obj, deployment, nil); err != nil {
				panic(err)
			}
			// Modify manager container command to match openshift Dockerfile
			for i := range deployment.Spec.Template.Spec.Containers {
				container := &deployment.Spec.Template.Spec.Containers[i]
				if container.Name == "manager" {
					container.Command = []string{"./bin/cluster-api-operator"}
				}
			}

			if err := scheme.Convert(deployment, &obj, nil); err != nil {
				panic(err)
			}

			finalObjs = append(finalObjs, obj)
		default:
			finalObjs = append(finalObjs, obj)
		}
	}
	return finalObjs
}

func writeAllOtherOperatorComponents(objs []unstructured.Unstructured) error {
	for _, obj := range objs {
		content, err := yaml.Marshal(obj.UnstructuredContent())
		if err != nil {
			return fmt.Errorf("failed to marshal object: %w", err)
		}

		fileName := fmt.Sprintf("%s.yaml", strings.ToLower(obj.GroupVersionKind().Kind))
		err = os.WriteFile(path.Join(operatorAssetsPath, fileName), ensureNewLine(content), 0600)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", operatorAssetsPath, err)
		}
	}

	return nil
}
