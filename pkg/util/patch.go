package util

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func IsPatchRequired(origObj client.Object, patch client.Patch) (bool, error) {
	data, err := patch.Data(origObj)
	if err != nil {
		return false, fmt.Errorf("failed to calculate patch: %w", err)
	}

	return string(data) != "{}", nil
}
