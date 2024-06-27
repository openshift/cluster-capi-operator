package util

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const MAPIV1Beta1ConversionDataAnnotationKey = "cluster.x-k8s.io/conversion-data/openshift/machine/v1beta1"

func GetAnnotationValueFromSourceObject(src any) (string, error) {
	b, err := json.Marshal(src)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal object: %w", err)
	}

	return string(b), nil
}
