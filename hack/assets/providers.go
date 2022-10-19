package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/version"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	configclient "sigs.k8s.io/cluster-api/cmd/clusterctl/client/config"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/repository"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
	utilyaml "sigs.k8s.io/cluster-api/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	powerVSProvider  = "powervs"
	ibmCloudProvider = "ibmcloud"
)

type provider struct {
	Name       string                    `json:"name"`
	PType      clusterctlv1.ProviderType `json:"type"`
	Branch     string                    `json:"branch"`
	Version    string                    `json:"version"`
	components repository.Components
	metadata   []byte
}

// writeProvidersCM validates and writes the provider configmap
func writeProvidersCM(providerList []byte) error {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster-capi-operator-providers",
			Namespace:   targetNamespace,
			Annotations: openshifAnnotations,
		},
		Data: map[string]string{
			"providers-list.yaml": string(providerList),
		},
	}

	cm.Annotations[techPreviewAnnotation] = techPreviewAnnotationValue

	cmYaml, err := yaml.Marshal(cm)
	if err != nil {
		return err
	}

	fName := fmt.Sprintf("%s02_providers.configmap.yaml", manifestPrefix)
	return os.WriteFile(path.Join(manifestsPath, fName), ensureNewLine(cmYaml), 0600)
}

// loadProviders load provider list from provider_config.yaml
func loadProviders(providerList []byte, providerName string) ([]provider, error) {
	providers := []provider{}
	if err := yaml.Unmarshal(providerList, &providers); err != nil {
		return nil, err
	}

	if providerName != "" {
		for _, p := range providers {
			if p.Name == providerName {
				return []provider{p}, nil
			}
		}
		return nil, fmt.Errorf("provider %s not found", providerName)
	}

	return providers, nil
}

func (p *provider) getMetadataUrl() string {
	providerPath := ""
	if p.PType == clusterctlv1.InfrastructureProviderType {
		providerPath = fmt.Sprintf("-provider-%s", p.Name)
	}

	return fmt.Sprintf("https://raw.githubusercontent.com/openshift/cluster-api%s/%s/metadata.yaml",
		providerPath,
		p.Branch,
	)
}

func (p *provider) getProviderAssetUrl() string {
	providerPath := ""
	if p.PType == clusterctlv1.InfrastructureProviderType {
		providerPath = fmt.Sprintf("-provider-%s", p.Name)
	}
	return fmt.Sprintf("https://github.com/openshift/cluster-api%s//config/default?ref=%s",
		providerPath,
		p.Branch,
	)
}

// loadComponents loads components from the given provider.
func (p *provider) loadComponents() error {
	if p.Branch == "" {
		return fmt.Errorf("provider %s has no branch", p.Name)
	}

	// Create new clusterctl config client
	configClient, err := configclient.New("")
	if err != nil {
		return err
	}

	// Create new clusterctl provider client
	providerConfig, err := configClient.Providers().Get(p.Name, p.PType)
	if err != nil {
		return err
	}

	// Set options for yaml processor
	options := repository.ComponentsOptions{
		TargetNamespace:     targetNamespace,
		SkipTemplateProcess: true,
	}

	// Download and compile assets using kustomize
	rawComponents, err := fetchAndCompileComponents(p.getProviderAssetUrl())
	if err != nil {
		return err
	}

	// Ininitialize new clusterctl repository components, this should some yaml processing
	p.components, err = repository.NewComponents(repository.ComponentsInput{
		Provider:     providerConfig,
		ConfigClient: configClient,
		Processor:    yamlprocessor.NewSimpleProcessor(),
		RawYaml:      rawComponents,
		Options:      options,
	})
	if err != nil {
		return err
	}

	metadataResponse, err := http.Get(p.getMetadataUrl())
	if err != nil {
		return err
	}
	defer metadataResponse.Body.Close()

	// Download metadata from github as raw yaml
	rawMetadataResponse, err := io.ReadAll(metadataResponse.Body)
	if err != nil {
		return err
	}
	p.metadata = rawMetadataResponse

	return err
}

func (p *provider) providerTypeName() string {
	return strings.ReplaceAll(strings.ToLower(string(p.PType)), "provider", "")
}

func (p *provider) writeAllOtherProviderComponents(objs []unstructured.Unstructured) error {
	if _, err := version.ParseSemantic(p.Version); err != nil {
		return fmt.Errorf("invalid version %s for provider %s, check provider-list.yaml", p.Version, p.Name)
	}

	combined, err := utilyaml.FromUnstructured(objs)
	if err != nil {
		return err
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: targetNamespace,
			Labels: map[string]string{
				"provider.cluster.x-k8s.io/name":    p.Name,
				"provider.cluster.x-k8s.io/type":    p.providerTypeName(),
				"provider.cluster.x-k8s.io/version": p.Version,
			},
		},
		Data: map[string]string{
			"metadata":   string(p.metadata),
			"components": string(combined),
		},
	}

	cmYaml, err := yaml.Marshal(cm)
	if err != nil {
		return err
	}

	fName := strings.ToLower(p.providerTypeName() + "-" + p.Name + ".yaml")
	assetsPath := providersAssetsPath
	if p.Name == "cluster-api" {
		assetsPath = coreCAPIAssetsPath
	}

	return os.WriteFile(path.Join(assetsPath, fName), ensureNewLine(cmYaml), 0600)
}

func (p *provider) writeProviders() error {
	assetsPath := providersAssetsPath
	var obj client.Object
	switch p.providerTypeName() {
	case "core":
		obj = &operatorv1.CoreProvider{
			TypeMeta: metav1.TypeMeta{Kind: "CoreProvider", APIVersion: "operator.cluster.x-k8s.io/v1alpha1"},
			Spec:     operatorv1.CoreProviderSpec{ProviderSpec: p.providerSpec()},
		}
		assetsPath = coreCAPIAssetsPath
	case "controlplane":
		obj = &operatorv1.ControlPlaneProvider{
			TypeMeta: metav1.TypeMeta{Kind: "ControlPlaneProvider", APIVersion: "operator.cluster.x-k8s.io/v1alpha1"},
			Spec:     operatorv1.ControlPlaneProviderSpec{ProviderSpec: p.providerSpec()},
		}
	case "bootstrap":
		obj = &operatorv1.BootstrapProvider{
			TypeMeta: metav1.TypeMeta{Kind: "BootstrapProvider", APIVersion: "operator.cluster.x-k8s.io/v1alpha1"},
			Spec:     operatorv1.BootstrapProviderSpec{ProviderSpec: p.providerSpec()},
		}
	case "infrastructure":
		obj = &operatorv1.InfrastructureProvider{
			TypeMeta: metav1.TypeMeta{Kind: "InfrastructureProvider", APIVersion: "operator.cluster.x-k8s.io/v1alpha1"},
			Spec:     operatorv1.InfrastructureProviderSpec{ProviderSpec: p.providerSpec()},
		}
	}
	obj.SetName(p.Name)
	obj.SetNamespace(targetNamespace)

	cmYaml, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}

	fName := strings.ToLower(p.providerTypeName() + "-" + p.Name + "-provider.yaml")
	return os.WriteFile(path.Join(assetsPath, fName), ensureNewLine(cmYaml), 0600)
}

func (p *provider) providerSpec() operatorv1.ProviderSpec {
	managerCommandPrefix := "cluster-api"

	if p.Name != "cluster-api" {
		managerCommandPrefix = fmt.Sprintf("cluster-api-provider-%s", p.Name)
	}

	managerCommand := fmt.Sprintf("./bin/%s-controller-manager", managerCommandPrefix)
	return operatorv1.ProviderSpec{
		FetchConfig: &operatorv1.FetchConfiguration{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"provider.cluster.x-k8s.io/name": p.Name,
					"provider.cluster.x-k8s.io/type": p.providerTypeName(),
				},
			},
		},
		Deployment: &operatorv1.DeploymentSpec{
			Containers: []operatorv1.ContainerSpec{
				{
					Name:    "manager",
					Command: []string{managerCommand},
				},
			},
		},
		Version: p.Version,
	}
}

func importProviders(providerName string) error {
	// Read provider list yaml
	providerList, err := ioutil.ReadFile(providerListPath)
	if err != nil {
		return fmt.Errorf("failed to read provider list: %v", err)
	}

	// Load provider list from conifg file
	providers, err := loadProviders(providerList, providerName)
	if err != nil {
		return err
	}
	// Write provider list config map to manifests
	if err := writeProvidersCM(providerList); err != nil {
		return fmt.Errorf("failed to write providers configmap: %v", err)
	}

	for _, p := range providers {
		fmt.Printf("Processing provider %s: %s\n", p.PType, p.Name)

		// Load manifests from github for specific provider

		// for Power VS the upstream cluster api provider name is ibmcloud
		// https://github.com/kubernetes-sigs/cluster-api/blob/main/cmd/clusterctl/client/config/providers_client.go#L210-L214
		var initialProviderName string
		if p.Name == powerVSProvider {
			initialProviderName = powerVSProvider
			p.Name = ibmCloudProvider
		}
		err := p.loadComponents()
		if err != nil {
			return err
		}
		if providerName == powerVSProvider {
			providerName = ibmCloudProvider
		}

		// Perform all manifest transformations

		// We need to perform Power VS specific customization which may not needed for ibmcloud
		if initialProviderName == powerVSProvider {
			p.Name = powerVSProvider
		}
		resourceMap := processObjects(p.components.Objs(), p.Name)

		// Write RBAC components to manifests, they will be managed by CVO
		if p.Name == powerVSProvider {
			p.Name = ibmCloudProvider
		}
		rbacFileName := fmt.Sprintf("%s03_rbac-roles.%s-%s.yaml", manifestPrefix, p.providerTypeName(), p.Name)
		err = writeComponentsToManifests(rbacFileName, resourceMap[rbacKey])
		if err != nil {
			return err
		}

		// Write CRD components to manifests, they will be managed by CVO
		crdFileName := fmt.Sprintf("%s02_crd.%s-%s.yaml", manifestPrefix, p.providerTypeName(), p.Name)
		err = writeComponentsToManifests(crdFileName, resourceMap[crdKey])
		if err != nil {
			return err
		}

		// Write all other components(deployments, services, secret, etc) to a config map,
		// they will be managed by CAPI operator
		err = p.writeAllOtherProviderComponents(resourceMap[otherKey])
		if err != nil {
			return err
		}

		// Write Cluster API Operator objects that represent providers
		err = p.writeProviders()
		if err != nil {
			return err
		}
	}
	return nil
}
