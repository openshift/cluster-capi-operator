package probing

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FieldsEqualProbe checks if the values of the fields under the given json paths are equal.
type FieldsEqualProbe struct {
	FieldA, FieldB string
}

var _ Prober = (*FieldsEqualProbe)(nil)

// Probe executes the probe.
func (fe *FieldsEqualProbe) Probe(obj client.Object) Result {
	return probeUnstructuredSingleMsg(obj, fe.probe)
}

func (fe *FieldsEqualProbe) probe(obj *unstructured.Unstructured) Result {
	fieldAPath := strings.Split(strings.Trim(fe.FieldA, "."), ".")
	fieldBPath := strings.Split(strings.Trim(fe.FieldB, "."), ".")

	fieldAVal, ok, err := unstructured.NestedFieldCopy(obj.Object, fieldAPath...)
	if err != nil || !ok {
		return Result{
			Status:   StatusFalse,
			Messages: []string{fmt.Sprintf(`"%v" missing`, fe.FieldA)},
		}
	}

	fieldBVal, ok, err := unstructured.NestedFieldCopy(obj.Object, fieldBPath...)
	if err != nil || !ok {
		return Result{
			Status:   StatusFalse,
			Messages: []string{fmt.Sprintf(`"%v" missing`, fe.FieldB)},
		}
	}

	if !equality.Semantic.DeepEqual(fieldAVal, fieldBVal) {
		//nolint:errchkjson
		fieldAJSON, _ := json.Marshal(fieldAVal)
		//nolint:errchkjson
		fieldBJSON, _ := json.Marshal(fieldBVal)

		return Result{
			Status:   StatusFalse,
			Messages: []string{fmt.Sprintf(`"%s" != "%s" expected: %s got: %s`, fe.FieldA, fe.FieldB, fieldAJSON, fieldBJSON)},
		}
	}

	return Result{
		Status:   StatusTrue,
		Messages: []string{fmt.Sprintf(`"%s" == "%s"`, fe.FieldA, fe.FieldB)},
	}
}
