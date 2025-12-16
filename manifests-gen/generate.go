package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"maps"
	"os"
	"path"

	"github.com/openshift/cluster-capi-operator/pkg/metadata"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

var errObjectTooLarge = errors.New("single object exceeds configMap size limit")

const (
	// customizedComponentsFilename is a name for file containing customized infrastructure components.
	// This file helps with code review as it is always uncompressed unlike the components configMap.
	customizedComponentsFilename = "infrastructure-components-openshift.yaml"

	// manifestPrefix is the prefix for the generated manifest file
	manifestPrefix = "0000_30_cluster-api_"

	// releaseVersionSubstitution is a magic string that `oc adm release new`
	// substitutes in the release image when it copies manifests from component
	// images.
	releaseVersionSubstitution = "0.0.1-snapshot"

	// configMapDataLimit is the size limit for data in a configMap.
	configMapDataLimit = 1024 * 1000 // 1MB - some headroom for the rest of the configMap
)

func generateManifestBundle(opts cmdlineOptions) error {
	fmt.Printf("Processing provider %s\n", opts.name)

	kustomizeDir := path.Join(opts.basePath, opts.kustomizeDir)
	resources, err := generateKustomizeResources(kustomizeDir)
	if err != nil {
		return err
	}

	// Perform all manifest transformations
	resources = processObjects(resources, opts.name)

	// Generate InfraCluster protection policy if an infrastructure cluster resource name is provided
	if opts.infraClusterResource != "" {
		resources = append(resources, generateInfraClusterProtectionPolicy(opts.infraClusterResource)...)
	}

	hasher := sha256.New()

	// Generate ConfigMaps
	// generateConfigMaps also writes the infrastructure components file as a side effect
	configMaps, err := generateConfigMaps(opts, hasher, resources)
	if err != nil {
		return fmt.Errorf("error generating config maps: %w", err)
	}

	// Write the transport ConfigMaps to a manifest file for CVO
	if err := writeConfigmaps(opts, hasher, configMaps); err != nil {
		return fmt.Errorf("error writing provider ConfigMap: %w", err)
	}

	return nil
}

// generateKustomizeResources generates resources from a kustomize directory
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

// writeConfigmaps writes the transport ConfigMaps to the given fileName. It
// adds all necessary annotations and labels.
func writeConfigmaps(opts cmdlineOptions, hasher hash.Hash, configMaps []*corev1.ConfigMap) (err error) {
	hashValue := fmt.Sprintf("%x", hasher.Sum(nil))

	annotations := map[string]string{
		metadata.CAPIOperatorProviderNameKey:    opts.name,
		metadata.CAPIOperatorProviderVersionKey: opts.version,
		metadata.CAPIOperatorContentIDKey:       hashValue,

		metadata.CAPIOperatorBundleSizeKey: fmt.Sprintf("%d", len(configMaps)),

		// CVO annotations
		"exclude.release.openshift.io/internal-openshift-hosted":      "true",
		"include.release.openshift.io/self-managed-high-availability": "true",
		"include.release.openshift.io/single-node-developer":          "true",

		// The feature set annotation is indicates to CVO when this configmap should be installed.
		"release.openshift.io/feature-set": "CustomNoUpgrade,TechPreviewNoUpgrade",
	}

	labels := map[string]string{
		metadata.CAPIOperatorProviderTypeKey: opts.providerType,
		metadata.CAPIOperatorPlatformKey:     opts.platform,

		// The release annotation is used to identify which release generated the manifests.
		metadata.CAPIOperatorOpenshiftReleaseKey: releaseVersionSubstitution,
	}

	// Don't write empty labels or annotations
	maps.DeleteFunc(annotations, emptyValue)
	maps.DeleteFunc(labels, emptyValue)

	cmFileName := fmt.Sprintf("%s04_cm.%s-%s.yaml", manifestPrefix, opts.providerType, opts.name)
	cmFile, err := os.OpenFile(path.Join(opts.manifestsPath, cmFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("error opening output file %s: %w", cmFileName, err)
	}

	writer := bufio.NewWriter(cmFile)
	defer func() {
		err = errors.Join(err,
			writer.Flush(),
			cmFile.Close(),
		)
	}()

	for i, cm := range configMaps {
		cm.SetAnnotations(maps.Clone(annotations))
		cm.SetLabels(labels)

		cm.Annotations["cluster-api.openshift.io/bundle-index"] = fmt.Sprintf("%d", i)

		data, err := yaml.Marshal(cm)
		if err != nil {
			return fmt.Errorf("error marshalling ConfigMap to YAML: %w", err)
		}

		for _, v := range yamlSeparated(i, data) {
			_, err := writer.Write(v)
			if err != nil {
				return fmt.Errorf("error writing ConfigMap to file: %w", err)
			}
		}
	}

	return nil
}

func generateConfigMaps(opts cmdlineOptions, hasher hash.Hash, resources []client.Object) (_ []*corev1.ConfigMap, errRet error) {
	infraComponentsPathname := path.Join(opts.basePath, "openshift", customizedComponentsFilename)
	infraComponentsFile, err := os.OpenFile(infraComponentsPathname, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("error opening infrastructure components file %s: %w", infraComponentsPathname, err)
	}
	defer func() {
		errRet = errors.Join(errRet, infraComponentsFile.Close())
	}()

	generateCM := cmGenerator(opts.name)
	var cmBuffer bytes.Buffer
	var configMaps []*corev1.ConfigMap

	for _, resource := range resources {
		data, err := yaml.Marshal(resource)
		if err != nil {
			return nil, fmt.Errorf("error marshalling object to YAML: %w", err)
		}

		var objBuffer bytes.Buffer
		objBuffer.WriteString("---\n")
		objBuffer.Write(data)

		if objBuffer.Len() > configMapDataLimit {
			return nil, errObjectTooLarge
		}

		hasher.Write(objBuffer.Bytes())

		if _, err := infraComponentsFile.Write(objBuffer.Bytes()); err != nil {
			return nil, fmt.Errorf("error writing object to file: %w", err)
		}

		if cmBuffer.Len()+objBuffer.Len() > configMapDataLimit {
			configMaps = append(configMaps, generateCM(cmBuffer))
			cmBuffer.Reset()
		}

		cmBuffer.Write(objBuffer.Bytes())
	}

	if cmBuffer.Len() > 0 {
		configMaps = append(configMaps, generateCM(cmBuffer))
	}

	return configMaps, nil
}

func cmGenerator(basename string) func(bytes.Buffer) *corev1.ConfigMap {
	index := 0

	return func(data bytes.Buffer) *corev1.ConfigMap {
		// NOTE: It might look tider to use '-' as the index separator, but this
		// breaks the release version substitution.
		name := fmt.Sprintf(basename+"-"+releaseVersionSubstitution+".%d", index)
		index++

		return &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Data: map[string]string{
				"components": data.String(),
			},
		}
	}
}

func emptyValue[K any](_ K, v string) bool {
	return v == ""
}

func yamlSeparated(i int, data []byte) [][]byte {
	if i == 0 {
		return [][]byte{data}
	} else {
		return [][]byte{[]byte("---\n"), data}
	}
}
