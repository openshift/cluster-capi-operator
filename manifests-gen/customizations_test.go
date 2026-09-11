package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestProcessObjectsDoesNotEmitNamespace(t *testing.T) {
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
