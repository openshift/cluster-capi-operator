package main

import (
	"io/ioutil"
	"os"
	"path"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "sigs.k8s.io/cluster-api/util/yaml"
)

var (
	assetsDir   = path.Join(projDir, "assets", "capi-operator")
	outFile     = path.Join(projDir, "manifests", "0000_30_cluster-api-operator_03_rbac_roles.yaml")
	annotations = map[string]string{
		"exclude.release.openshift.io/internal-openshift-hosted":      "true",
		"include.release.openshift.io/self-managed-high-availability": "true",
		"include.release.openshift.io/single-node-developer":          "true",
	}
)

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

		// TODO if obj.Name == "capi-operator-manager-role" set specific roles
		// not current "*".
		switch obj.GetKind() {
		case "ClusterRole", "Role", "ClusterRoleBinding", "RoleBinding", "ServiceAccount":
			anno := obj.GetAnnotations()
			if anno == nil {
				anno = map[string]string{}
			}
			for k, v := range annotations {
				anno[k] = v
			}
			obj.SetAnnotations(anno)
			roles = append(roles, obj)
		default:
		}
	}
	return roles, nil
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
	b = []byte(string(b) + "\n") // add a new line for git
	return os.WriteFile(outFile, b, 0600)
}
