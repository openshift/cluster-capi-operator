package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

const (
	// manifestsFilename is the name of the file containing the generated manifests.
	manifestsFilename = "manifests.yaml"

	// metadataFilename is the name of the file containing provider metadata.
	metadataFilename = "metadata.yaml"

	// capiNamespace is the namespace where capi components are created.
	capiNamespace = "openshift-cluster-api"
)

func generateManifests(opts cmdlineOptions) error {
	fmt.Printf("Processing provider %s\n", opts.name)

	resources, err := generateKustomizeResources(opts.kustomizeDir)
	if err != nil {
		return fmt.Errorf("failed to generate kustomize resources: %w", err)
	}

	if err != nil {
		return fmt.Errorf("failed to genereate kustomize resources: %w", err)
	}

	// Perform all manifest transformations
	resources, err = processObjects(resources, opts)
	if err != nil {
		return fmt.Errorf("error processing objects: %w", err)
	}

	// Write the manifest file
	if err := writeManifests(opts, resources); err != nil {
		return fmt.Errorf("error writing manifests: %w", err)
	}

	// Write the metadata file
	if err := writeMetadata(opts); err != nil {
		return fmt.Errorf("error writing metadata: %w", err)
	}

	return nil
}

// generateKustomizeResources generates resources from a kustomize directory.
func generateKustomizeResources(kustomizeDir string) ([]client.Object, error) {
	// Compile assets using kustomize.
	fmt.Printf("> Generating OpenShift manifests based on kustomize.yaml from %q\n", kustomizeDir)

	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	fSys := filesys.MakeFsOnDisk()

	res, err := k.Run(fSys, kustomizeDir)
	if err != nil {
		return nil, fmt.Errorf("error fetching and compiling assets using kustomize: %w", err)
	}

	resources := make([]client.Object, 0, len(res.Resources()))
	for _, resource := range res.Resources() {
		if resource == nil {
			continue
		}

		data, err := resource.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("error marshalling resource to JSON: %w", err)
		}

		var unstructured unstructured.Unstructured
		err = json.Unmarshal(data, &unstructured)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling resource to unstructured: %w", err)
		}

		resources = append(resources, &unstructured)
	}

	return resources, nil
}

func writeManifests(opts cmdlineOptions, resources []client.Object) (err error) {
	manifestsDir := path.Join(opts.manifestsPath, opts.profileName)

	err = os.MkdirAll(manifestsDir, os.ModeDir|0755)
	if err != nil {
		return fmt.Errorf("error creating metadata directory %s: %w", manifestsDir, err)
	}

	manifestsPathname := path.Join(manifestsDir, manifestsFilename)

	manifestsFile, err := os.OpenFile(manifestsPathname, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("error opening manifests file %s: %w", manifestsPathname, err)
	}

	writer := bufio.NewWriter(manifestsFile)
	defer func() {
		err = errors.Join(err,
			writer.Flush(),
			manifestsFile.Close())
	}()

	for i, resource := range resources {
		data, err := yaml.Marshal(resource)
		if err != nil {
			return fmt.Errorf("error marshalling object to YAML: %w", err)
		}

		if i > 0 {
			if _, err := writer.Write([]byte("---\n")); err != nil {
				return fmt.Errorf("error writing separator to manifests file: %w", err)
			}
		}

		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("error writing object to manifests file: %w", err)
		}
	}

	return nil
}

func writeMetadata(opts cmdlineOptions) (err error) {
	metadataDir := path.Join(opts.manifestsPath, opts.profileName)

	err = os.MkdirAll(metadataDir, os.ModeDir|0755)
	if err != nil {
		return fmt.Errorf("error creating metadata directory %s: %w", metadataDir, err)
	}

	metadataPathname := path.Join(metadataDir, metadataFilename)

	metadataFile, err := os.OpenFile(metadataPathname, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("error opening metadata file %s: %w", metadataPathname, err)
	}
	defer func() {
		err = errors.Join(err, metadataFile.Close())
	}()

	metadata := providerimages.ProviderMetadata{
		ProviderName:     opts.name,
		ProviderType:     opts.providerType,
		ProviderVersion:  opts.version,
		OCPPlatform:      configv1.PlatformType(opts.platform),
		ProviderImageRef: opts.providerImageRef,
	}

	data, err := yaml.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("error marshalling metadata to YAML: %w", err)
	}
	_, err = metadataFile.Write(data)
	if err != nil {
		return fmt.Errorf("error writing metadata to file: %w", err)
	}
	return nil
}
