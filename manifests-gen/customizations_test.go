package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func TestProcessObjectsSecretFiltering(t *testing.T) {
	newObject := func(kind string) client.Object {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion("v1")
		obj.SetKind(kind)
		obj.SetName(kind)
		obj.SetNamespace("openshift-cluster-api")
		return obj
	}

	secret := newObject("Secret")
	secret.SetName("test-kubeconfig")
	otherSecret := newObject("Secret")
	otherSecret.SetName("test-credentials")
	objects := []client.Object{
		secret,
		otherSecret,
		newObject("Namespace"),
		newObject("ConfigMap"),
	}

	for _, tc := range []struct {
		name      string
		wantNames []string
	}{
		{name: "only kubeconfig secrets retained", wantNames: []string{"test-kubeconfig", "ConfigMap"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := processObjects(objects, cmdlineOptions{})
			if err != nil {
				t.Fatalf("processObjects() error = %v", err)
			}

			gotNames := make([]string, 0, len(got))
			for _, obj := range got {
				gotNames = append(gotNames, obj.GetName())
			}
			if len(gotNames) != len(tc.wantNames) {
				t.Fatalf("got names %v, want %v", gotNames, tc.wantNames)
			}
			for i := range tc.wantNames {
				if gotNames[i] != tc.wantNames[i] {
					t.Fatalf("got names %v, want %v", gotNames, tc.wantNames)
				}
			}
		})
	}
}

func TestManagementKubeconfigSecretValue(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	manifestPath := filepath.Join(filepath.Dir(thisFile), "..", "ocp-manifests-input", "kubeconfig", "management-cluster-kubeconfig-secret.yaml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading Secret manifest: %v", err)
	}
	var secret struct {
		Data struct {
			Value string `yaml:"value"`
		} `yaml:"data"`
	}
	if err := yaml.Unmarshal(manifest, &secret); err != nil {
		t.Fatalf("decoding Secret manifest: %v", err)
	}
	value, err := base64.StdEncoding.DecodeString(secret.Data.Value)
	if err != nil {
		t.Fatalf("decoding Secret data.value: %v", err)
	}
	kubeconfig, err := clientcmd.Load(value)
	if err != nil {
		t.Fatalf("loading kubeconfig: %v", err)
	}
	cluster := kubeconfig.Clusters["management-cluster"]
	if cluster == nil || cluster.Server != "https://kubernetes.default.svc:443" || cluster.CertificateAuthority != "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt" || cluster.CertificateAuthorityData != nil {
		t.Fatalf("unexpected cluster configuration: %#v", cluster)
	}
	user := kubeconfig.AuthInfos["service-account"]
	if user == nil || user.TokenFile != "/var/run/secrets/kubernetes.io/serviceaccount/token" || user.Token != "" {
		t.Fatalf("unexpected user configuration: %#v", user)
	}
}

func TestProcessObjectsDoesNotMutateNamespaceBehavior(t *testing.T) {
	namespace := &unstructured.Unstructured{}
	namespace.SetAPIVersion("v1")
	namespace.SetKind("Namespace")
	namespace.SetName("test")

	got, err := processObjects([]client.Object{namespace}, cmdlineOptions{})
	if err != nil {
		t.Fatalf("processObjects() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d objects, want Namespace excluded", len(got))
	}
}
