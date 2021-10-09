package controllers

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/utils/pointer"
	operatorv1 "sigs.k8s.io/cluster-api/exp/operator/api/v1alpha1"
)

func TestNewImageMeta(t *testing.T) {
	tests := []struct {
		name     string
		imageURL string
		want     *operatorv1.ImageMeta
	}{
		{
			name:     "infrastructure-aws:manager",
			imageURL: "k8s.gcr.io/cluster-api-aws/cluster-api-aws-controller:v0.7.0",
			want: &operatorv1.ImageMeta{
				Name:       pointer.StringPtr("cluster-api-aws-controller"),
				Repository: pointer.StringPtr("k8s.gcr.io/cluster-api-aws"),
				Tag:        pointer.StringPtr("v0.7.0"),
			},
		},
		{
			name:     "infrastructure-azure:manager",
			imageURL: "us.gcr.io/k8s-artifacts-prod/cluster-api-azure/cluster-api-azure-controller:v0.5.2",
			want: &operatorv1.ImageMeta{
				Name:       pointer.StringPtr("cluster-api-azure-controller"),
				Repository: pointer.StringPtr("us.gcr.io/k8s-artifacts-prod/cluster-api-azure"),
				Tag:        pointer.StringPtr("v0.5.2"),
			},
		},
		{
			name:     "infrastructure-metal3:ip-address-manager",
			imageURL: "quay.io/metal3-io/ip-address-manager:v0.1.0",
			want: &operatorv1.ImageMeta{
				Name:       pointer.StringPtr("ip-address-manager"),
				Repository: pointer.StringPtr("quay.io/metal3-io"),
				Tag:        pointer.StringPtr("v0.1.0"),
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
	sampleImagesFile := filepath.Clean("../../hack/sample-images.json")
	jsonData, err := ioutil.ReadFile(sampleImagesFile)
	if err != nil {
		t.Fatal("unable to read file", sampleImagesFile, err)
	}
	containerImages := map[string]string{}
	if err := json.Unmarshal(jsonData, &containerImages); err != nil {
		t.Fatal("unable to unmarshal image names from file", sampleImagesFile, err)
	}
	tests := []struct {
		name  string
		pKind string
		pName string
		want  []operatorv1.ContainerSpec
	}{
		{
			name:  "cluster-api",
			pKind: "CoreProvider",
			pName: "cluster-api",
			want: []operatorv1.ContainerSpec{
				{
					Name: "manager",
					Image: &operatorv1.ImageMeta{
						Name:       pointer.StringPtr("cluster-api-controller"),
						Repository: pointer.StringPtr("k8s.gcr.io/cluster-api"),
						Tag:        pointer.StringPtr("v0.4.3"),
					},
				},
			},
		},
		{
			name:  "aws",
			pKind: "InfrastructureProvider",
			pName: "aws",
			want: []operatorv1.ContainerSpec{
				{
					Name: "manager",
					Image: &operatorv1.ImageMeta{
						Name:       pointer.StringPtr("cluster-api-aws-controller"),
						Repository: pointer.StringPtr("k8s.gcr.io/cluster-api-aws"),
						Tag:        pointer.StringPtr("v0.7.0"),
					},
				},
				{
					Name: "kube-rbac-proxy",
					Image: &operatorv1.ImageMeta{
						Name:       pointer.StringPtr("kube-rbac-proxy"),
						Repository: pointer.StringPtr("gcr.io/kubebuilder"),
						Tag:        pointer.StringPtr("v0.8.0"),
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
			if got := r.containerCustomizationFromProvider(tt.pKind, tt.pName); !reflect.DeepEqual(got, tt.want) {
				t.Error(cmp.Diff(got, tt.want))
			}
		})
	}
}
