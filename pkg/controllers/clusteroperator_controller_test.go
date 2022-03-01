package controllers

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
)

func TestNewImageMeta(t *testing.T) {
	tests := []struct {
		name     string
		imageURL string
		want     *operatorv1.ImageMeta
	}{
		{
			name:     "infrastructure-aws:manager",
			imageURL: "quay.io/ademicev/cluster-api-aws/cluster-api-aws-controller:v0.7.0",
			want: &operatorv1.ImageMeta{
				Name:       "cluster-api-aws-controller",
				Repository: "quay.io/ademicev/cluster-api-aws",
				Tag:        "v0.7.0",
			},
		},
		{
			name:     "infrastructure-azure:manager",
			imageURL: "us.gcr.io/k8s-artifacts-prod/cluster-api-azure/cluster-api-azure-controller:v0.5.2",
			want: &operatorv1.ImageMeta{
				Name:       "cluster-api-azure-controller",
				Repository: "us.gcr.io/k8s-artifacts-prod/cluster-api-azure",
				Tag:        "v0.5.2",
			},
		},
		{
			name:     "infrastructure-metal3:ip-address-manager",
			imageURL: "quay.io/metal3-io/ip-address-manager:v0.1.0",
			want: &operatorv1.ImageMeta{
				Name:       "ip-address-manager",
				Repository: "quay.io/metal3-io",
				Tag:        "v0.1.0",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newImageMeta(tt.imageURL); !reflect.DeepEqual(got, tt.want) {
				t.Error(cmp.Diff(got, tt.want))
			}
		})
	}
}

func TestContainerCustomizationFromProvider(t *testing.T) {
	sampleImagesFile := filepath.Clean("../../dev-images.json")
	jsonData, err := ioutil.ReadFile(sampleImagesFile)
	if err != nil {
		t.Fatal("unable to read file", sampleImagesFile, err)
	}
	containerImages := map[string]string{}
	if err := json.Unmarshal(jsonData, &containerImages); err != nil {
		t.Fatal("unable to unmarshal image names from file", sampleImagesFile, err)
	}
	tests := []struct {
		name            string
		pKind           string
		pName           string
		inputContainers []operatorv1.ContainerSpec
		want            []operatorv1.ContainerSpec
	}{
		{
			name:  "cluster-api",
			pKind: "CoreProvider",
			pName: "cluster-api",
			inputContainers: []operatorv1.ContainerSpec{
				{
					Name: "manager",
				},
			},
			want: []operatorv1.ContainerSpec{
				{
					Name: "manager",
					Image: &operatorv1.ImageMeta{
						Name:       "cluster-api",
						Repository: "quay.io/ademicev",
						Tag:        "latest",
					},
				},
			},
		},
		{
			name:  "aws",
			pKind: "InfrastructureProvider",
			pName: "aws",
			inputContainers: []operatorv1.ContainerSpec{
				{
					Name: "manager",
				},
				{
					Name: "kube-rbac-proxy",
				},
			},
			want: []operatorv1.ContainerSpec{
				{
					Name: "manager",
					Image: &operatorv1.ImageMeta{
						Name:       "cluster-api-provider-aws",
						Repository: "quay.io/ademicev",
						Tag:        "latest",
					},
				},
				{
					Name: "kube-rbac-proxy",
					Image: &operatorv1.ImageMeta{
						Name:       "kube-rbac-proxy",
						Repository: "gcr.io/kubebuilder",
						Tag:        "v0.5.0",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ClusterOperatorReconciler{
				Images: containerImages,
			}
			if got := r.containerCustomizationFromProvider(tt.pKind, tt.pName, tt.inputContainers); !reflect.DeepEqual(got, tt.want) {
				t.Error(cmp.Diff(got, tt.want))
			}
		})
	}
}
