package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "sigs.k8s.io/cluster-api/util/yaml"
)

var (
	assetsDir   = path.Join(projDir, "assets", "capi-operator")
	outFile     = path.Join(projDir, "manifests", "0000_30_cluster-api_operator_03_rbac_roles.yaml")
	annotations = map[string]string{
		"exclude.release.openshift.io/internal-openshift-hosted":      "true",
		"include.release.openshift.io/self-managed-high-availability": "true",
		"include.release.openshift.io/single-node-developer":          "true",
	}
)

func upstreamOperatorRoles() []unstructured.Unstructured {
	writeVerbs := []string{"create", "delete", "get", "list", "patch", "update", "watch"}
	capiOperatorManagerRole := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "capi-operator-manager-role",
			Labels: map[string]string{
				"clusterctl.cluster.x-k8s.io/core": "capi-operator",
			},
			Annotations: map[string]string{},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     writeVerbs,
				APIGroups: []string{""},
				Resources: []string{"configmaps", "secrets", "services", "serviceaccounts"},
			},
			{
				Verbs:     writeVerbs,
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "daemonsets"},
			},
			{
				Verbs:     writeVerbs,
				APIGroups: []string{"admissionregistration.k8s.io"},
				Resources: []string{"validatingwebhookconfigurations", "mutatingwebhookconfigurations"},
			},
			{
				Verbs:     writeVerbs,
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
			},
			{
				Verbs:     writeVerbs,
				APIGroups: []string{"operator.cluster.x-k8s.io"},
				Resources: []string{"coreproviders", "infrastructureproviders", "controlplaneproviders", "bootstrapproviders"},
			},
			{
				Verbs:     writeVerbs,
				APIGroups: []string{"operator.cluster.x-k8s.io"},
				Resources: []string{"coreproviders/status", "infrastructureproviders/status", "controlplaneproviders/status", "bootstrapproviders/status"},
			},
			{
				Verbs:     writeVerbs,
				APIGroups: []string{"clusterctl.cluster.x-k8s.io"},
				Resources: []string{"providers"},
			},
		},
	}

	obj := unstructured.Unstructured{}
	if err := scheme.Convert(capiOperatorManagerRole, &obj, nil); err != nil {
		panic(err)
	}
	setOpenShiftAnnotations(obj, false)
	return []unstructured.Unstructured{obj}
}

func rbacObjects() ([]unstructured.Unstructured, error) {
	fileInfo, err := ioutil.ReadDir(assetsDir)
	if err != nil {
		return nil, err
	}

	roles := []unstructured.Unstructured{}
	for _, fi := range fileInfo {
		b, err := os.ReadFile(path.Join(assetsDir, fi.Name()))
		if err != nil {
			return nil, err
		}
		objs, err := utilyaml.ToUnstructured(b)
		if err != nil {
			return nil, err
		}

		obj := objs[0] // only one

		switch obj.GetKind() {
		case "ClusterRole", "Role", "ClusterRoleBinding", "RoleBinding", "ServiceAccount":
			if obj.GetName() == "capi-operator-manager-role" && obj.GetKind() == "ClusterRole" {
				// ignore this, and insert our own roles below.
				fmt.Println("skipping capi-operator-manager-role ", obj.GetKind())
				continue
			}
			fmt.Println("moving ", obj.GetName(), " ", obj.GetKind())
			setOpenShiftAnnotations(obj, true)
			roles = append(roles, obj)
		default:
		}
	}
	return append(roles, upstreamOperatorRoles()...), nil
}

func setOpenShiftAnnotations(obj unstructured.Unstructured, merge bool) {
	if !merge || len(obj.GetAnnotations()) == 0 {
		obj.SetAnnotations(annotations)
	}

	anno := obj.GetAnnotations()
	for k, v := range annotations {
		anno[k] = v
	}
	obj.SetAnnotations(anno)
}

func moveRBACToManifests() error {
	roles, err := rbacObjects()
	if err != nil {
		return err
	}
	b, err := utilyaml.FromUnstructured(roles)
	if err != nil {
		return err
	}
	return os.WriteFile(outFile, ensureNewLine(b), 0600)
}
