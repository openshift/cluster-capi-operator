package main

import (
	"strings"
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

func newClusterRole(name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("rbac.authorization.k8s.io/v1")
	obj.SetKind("ClusterRole")
	obj.SetName(name)
	return obj
}

func TestProcessObjectsAggregateClusterRoleEmptyRules(t *testing.T) {
	cr := newClusterRole("aggregate-role")
	cr.Object["aggregationRule"] = map[string]interface{}{
		"clusterRoleSelectors": []interface{}{
			map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "test",
				},
			},
		},
	}
	cr.Object["rules"] = []interface{}{}

	got, err := processObjects([]client.Object{cr}, cmdlineOptions{})
	if err != nil {
		t.Fatalf("processObjects() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d objects, want 1", len(got))
	}
	u := got[0].(*unstructured.Unstructured)
	if _, ok := u.Object["rules"]; ok {
		t.Fatal("expected rules field to be suppressed on aggregate ClusterRole with empty rules")
	}
}

func TestProcessObjectsAggregateClusterRoleNonEmptyRules(t *testing.T) {
	cr := newClusterRole("aggregate-role")
	cr.Object["aggregationRule"] = map[string]interface{}{
		"clusterRoleSelectors": []interface{}{
			map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "test",
				},
			},
		},
	}
	cr.Object["rules"] = []interface{}{
		map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"pods"},
			"verbs":     []interface{}{"get"},
		},
	}

	_, err := processObjects([]client.Object{cr}, cmdlineOptions{})
	if err == nil {
		t.Fatal("expected error for aggregate ClusterRole with non-empty rules")
	}
	if !strings.Contains(err.Error(), "aggregation controller") {
		t.Fatalf("error should mention aggregation controller, got: %v", err)
	}
}

func TestProcessObjectsAggregateClusterRoleNoRulesField(t *testing.T) {
	cr := newClusterRole("aggregate-role")
	cr.Object["aggregationRule"] = map[string]interface{}{
		"clusterRoleSelectors": []interface{}{
			map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "test",
				},
			},
		},
	}
	// No rules field at all

	got, err := processObjects([]client.Object{cr}, cmdlineOptions{})
	if err != nil {
		t.Fatalf("processObjects() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d objects, want 1", len(got))
	}
}

func TestProcessObjectsRegularClusterRole(t *testing.T) {
	cr := newClusterRole("regular-role")
	cr.Object["rules"] = []interface{}{
		map[string]interface{}{
			"apiGroups": []interface{}{""},
			"resources": []interface{}{"pods"},
			"verbs":     []interface{}{"get"},
		},
	}
	// No aggregationRule

	got, err := processObjects([]client.Object{cr}, cmdlineOptions{})
	if err != nil {
		t.Fatalf("processObjects() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d objects, want 1", len(got))
	}
	u := got[0].(*unstructured.Unstructured)
	rules, ok := u.Object["rules"]
	if !ok {
		t.Fatal("expected rules field to be retained on regular ClusterRole")
	}
	rulesList := rules.([]interface{})
	if len(rulesList) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rulesList))
	}
}
