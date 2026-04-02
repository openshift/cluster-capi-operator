package probing

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FieldValueProbe checks if the value found at FieldPath matches the provided Value.
type FieldValueProbe struct {
	FieldPath, Value string
}

var _ Prober = (*FieldValueProbe)(nil)

// Probe executes the probe.
func (fv *FieldValueProbe) Probe(obj client.Object) Result {
	return probeUnstructuredSingleMsg(obj, fv.probe)
}

func (fv *FieldValueProbe) probe(obj *unstructured.Unstructured) Result {
	fieldPath := strings.Split(strings.Trim(fv.FieldPath, "."), ".")

	fieldVal, ok, err := unstructured.NestedFieldCopy(obj.Object, fieldPath...)
	if err != nil {
		return Result{
			Status:   StatusFalse,
			Messages: []string{fmt.Sprintf(`error locating key %q; %v`, fv.FieldPath, err)},
		}
	}

	if !ok {
		return Result{
			Status:   StatusFalse,
			Messages: []string{fmt.Sprintf(`missing key: %q`, fv.FieldPath)},
		}
	}

	if !equality.Semantic.DeepEqual(fieldVal, fv.Value) {
		foundJSON, err := json.Marshal(fieldVal)
		if err != nil {
			foundJSON = []byte("<value marshal failed>")
		}

		expectedJSON, err := json.Marshal(fv.Value)
		if err != nil {
			expectedJSON = []byte("<value marshal failed>")
		}

		return Result{
			Status:   StatusFalse,
			Messages: []string{fmt.Sprintf(`value at key %q != %q; expected: %s got: %s`, fv.FieldPath, fv.Value, expectedJSON, foundJSON)},
		}
	}

	return Result{
		Status:   StatusTrue,
		Messages: []string{fmt.Sprintf(`value at key %q == %q`, fv.FieldPath, fv.Value)},
	}
}
