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
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	configclient "sigs.k8s.io/cluster-api/cmd/clusterctl/client/config"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/repository"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
	operatorv1 "sigs.k8s.io/cluster-api/exp/operator/api/v1alpha1"
	utilyaml "sigs.k8s.io/cluster-api/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

type provider struct {
	Name       string                    `json:"name"`
	PType      clusterctlv1.ProviderType `json:"type"`
	Branch     string                    `json:"branch"`
	components repository.Components
	metadata   []byte
}

// loadProviders load provider list from provider_config.yaml
func loadProviders() ([]provider, error) {
	yamlConfig, err := ioutil.ReadFile(providerListPath)
	if err != nil {
		return nil, err
	}

	providers := []provider{}
	if err := yaml.Unmarshal(yamlConfig, &providers); err != nil {
		return nil, err
	}

	return providers, nil
}

func (p *provider) getComponentsUrl() string {
	providerPath := ""
	componentsName := "core"
	if p.PType == clusterctlv1.InfrastructureProviderType {
		providerPath = fmt.Sprintf("-provider-%s", p.Name)
		componentsName = "infrastructure"
	}

	return fmt.Sprintf("https://raw.githubusercontent.com/openshift/cluster-api%s/%s/openshift/%s-components.yaml",
		providerPath,
		p.Branch,
		componentsName,
	)
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
		SkipTemplateProcess: false,
	}

	// Download provider components from github as raw yaml
	componentsResponse, err := http.Get(p.getComponentsUrl())
	if err != nil {
		return err
	}
	defer componentsResponse.Body.Close()
	rawComponentsResponse, err := io.ReadAll(componentsResponse.Body)
	if err != nil {
		return err
	}

	// Ininitialize new clusterctl repository components, this should some yaml processing
	p.components, err = repository.NewComponents(repository.ComponentsInput{
		Provider:     providerConfig,
		ConfigClient: configClient,
		Processor:    yamlprocessor.NewSimpleProcessor(),
		RawYaml:      rawComponentsResponse,
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
				"provider.cluster.x-k8s.io/name": p.Name,
				"provider.cluster.x-k8s.io/type": p.providerTypeName(),
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
	return operatorv1.ProviderSpec{
		FetchConfig: &operatorv1.FetchConfiguration{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"provider.cluster.x-k8s.io/name": p.Name,
					"provider.cluster.x-k8s.io/type": p.providerTypeName(),
				},
			},
		},
	}
}

func filterOutIPAM(objs []unstructured.Unstructured) []unstructured.Unstructured {
	finalObjs := []unstructured.Unstructured{}
	for _, obj := range objs {
		if obj.GetKind() == "CustomResourceDefinition" || !strings.Contains(strings.ToLower(obj.GetName()), "ipam") {
			finalObjs = append(finalObjs, obj)
		}
	}
	return finalObjs
}

func filterOutUnwantedResources(providerName string, objs []unstructured.Unstructured) []unstructured.Unstructured {
	// Filter out IPAM
	if providerName == "metal3" {
		objs = filterOutIPAM(objs)
	}

	return objs
}

func importProviders() error {
	// Load provider list from conifg file
	providers, err := loadProviders()
	if err != nil {
		return err
	}

	for _, p := range providers {
		fmt.Printf("Processing provider %s: %s\n", p.PType, p.Name)

		// Load manifests from github for specific provider
		err := p.loadComponents()
		if err != nil {
			return err
		}

		// Change cert-manager annotations to service-ca, because openshift doesn't support cert-manager
		objs := certManagerToServiceCA(p.components.Objs())

		// Filter out unwanted resources like IPAM
		objs = filterOutUnwantedResources(p.Name, objs)

		// Split out RBAC objects
		rbacObjs, crdObjs, allOtherObjs := splitRBACAndCRDsOut(objs)

		// Write RBAC components to manifests, they will be managed by CVO
		rbacFileName := fmt.Sprintf("%s%s-%s_03_rbac.yaml", manifestPrefix, p.providerTypeName(), p.Name)
		err = writeComponentsToManifests(rbacFileName, rbacObjs)
		if err != nil {
			return err
		}

		// Write CRD components to manifests, they will be managed by CVO
		crdFileName := fmt.Sprintf("%s%s-%s_02_crd.yaml", manifestPrefix, p.providerTypeName(), p.Name)
		err = writeComponentsToManifests(crdFileName, crdObjs)
		if err != nil {
			return err
		}

		// Write all other components(deployments, services, secret, etc) to a config map,
		// they will be managed by CAPI operator
		err = p.writeAllOtherProviderComponents(allOtherObjs)
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
