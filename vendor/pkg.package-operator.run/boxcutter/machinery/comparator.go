package machinery

import (
	"bytes"
	"fmt"
	"strings"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/openapi"
	"k8s.io/client-go/openapi3"
	"k8s.io/kube-openapi/pkg/schemaconv"
	"k8s.io/kube-openapi/pkg/spec3"
	"k8s.io/kube-openapi/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
	"sigs.k8s.io/structured-merge-diff/v6/typed"

	"pkg.package-operator.run/boxcutter/machinery/types"
)

// Comparator detects divergent state between desired and actual
// by comparing managed field ownerships.
// If not all fields from desired are owned by the same field owner in actual,
// we know that the object has been updated by another actor.
type Comparator struct {
	openAPIAccessor openAPIAccessor
	scheme          *runtime.Scheme
	fieldOwner      string
}

type discoveryClient interface {
	OpenAPIV3() openapi.Client
}

type openAPIAccessor interface {
	Get(gv schema.GroupVersion) (*spec3.OpenAPI, error)
}

// NewComparator returns a new Comparator instance.
func NewComparator(
	discoveryClient discoveryClient,
	scheme *runtime.Scheme,
	fieldOwner string,
) *Comparator {
	return &Comparator{
		openAPIAccessor: &defaultOpenAPIAccessor{
			c: discoveryClient.OpenAPIV3(),
		},
		scheme:     scheme,
		fieldOwner: fieldOwner,
	}
}

type defaultOpenAPIAccessor struct {
	c openapi.Client
}

func (a *defaultOpenAPIAccessor) Get(gv schema.GroupVersion) (*spec3.OpenAPI, error) {
	r := openapi3.NewRoot(a.c)

	return r.GVSpec(gv)
}

// CompareResult holds the results of a compare check.
type CompareResult struct {
	// ConflictingMangers is a list of all managers conflicting with "our" field manager.
	ConflictingMangers []CompareResultManagedFields
	// OtherManagers is a list of all other managers working on the object.
	OtherManagers []CompareResultManagedFields
	// DesiredFieldSet contains all fields identified to be
	// part of the desired object. It is used for conflict
	// detection with other field owners.
	DesiredFieldSet *fieldpath.Set
	// Comparison of desired fields to actual fields.
	Comparison *typed.Comparison
}

// CompareResultManagedFields combines a manger with the fields it manages.
type CompareResultManagedFields struct {
	// Manager causing the conflict.
	Manager string
	// Fields affected by this conflict.
	Fields *fieldpath.Set
}

func (d CompareResult) String() string {
	var out bytes.Buffer
	if len(d.ConflictingMangers) != 0 {
		fmt.Fprintln(&out, "Conflicts:")
	}

	for _, c := range d.ConflictingMangers {
		fmt.Fprintf(&out, "- %q\n", c.Manager)
		c.Fields.Iterate(func(p fieldpath.Path) {
			fmt.Fprintf(&out, "  %s\n", p.String())
		})
	}

	if len(d.OtherManagers) != 0 {
		fmt.Fprintln(&out, "Other:")
	}

	for _, c := range d.OtherManagers {
		fmt.Fprintf(&out, "- %q\n", c.Manager)
		c.Fields.Iterate(func(p fieldpath.Path) {
			fmt.Fprintf(&out, "  %s\n", p.String())
		})
	}

	if d.Comparison != nil {
		printAdded := d.Comparison.Added != nil && !d.Comparison.Added.Empty()
		printModified := d.Comparison.Modified != nil && !d.Comparison.Modified.Empty()
		printRemoved := d.Comparison.Removed != nil && !d.Comparison.Removed.Empty()

		if printAdded || printModified || printRemoved {
			fmt.Fprintln(&out, "Comparison:")
		}

		if printAdded {
			fmt.Fprintln(&out, "- Added:")
			d.Comparison.Added.Leaves().Iterate(func(p fieldpath.Path) {
				fmt.Fprintf(&out, "  %s\n", p.String())
			})
		}

		if printModified {
			fmt.Fprintln(&out, "- Modified:")
			d.Comparison.Modified.Leaves().Iterate(func(p fieldpath.Path) {
				fmt.Fprintf(&out, "  %s\n", p.String())
			})
		}

		if printRemoved {
			fmt.Fprintln(&out, "- Removed:")
			d.Comparison.Removed.Leaves().Iterate(func(p fieldpath.Path) {
				fmt.Fprintf(&out, "  %s\n", p.String())
			})
		}
	}

	return out.String()
}

// IsConflict returns true, if another actor has overidden changes.
func (d CompareResult) IsConflict() bool {
	return len(d.ConflictingMangers) > 0
}

// Modified returns a list of fields that have been modified.
func (d CompareResult) Modified() []string {
	if d.Comparison == nil {
		return nil
	}

	var out []string

	d.Comparison.Modified.Iterate(func(p fieldpath.Path) {
		out = append(out, p.String())
	})
	d.Comparison.Removed.Iterate(func(p fieldpath.Path) {
		out = append(out, p.String())
	})

	return out
}

// Compare checks if a resource has been changed from desired.
func (d *Comparator) Compare(
	desiredObject, actualObject Object,
	opts ...types.ComparatorOption,
) (res CompareResult, err error) {
	var options types.ComparatorOptions
	for _, opt := range opts {
		opt.ApplyToComparatorOptions(&options)
	}

	if err := ensureGVKIsSet(desiredObject, d.scheme); err != nil {
		return res, err
	}

	if err := ensureGVKIsSet(actualObject, d.scheme); err != nil {
		return res, err
	}

	desiredGVK := desiredObject.GetObjectKind().GroupVersionKind()
	actualGVK := actualObject.GetObjectKind().GroupVersionKind()

	if desiredGVK != actualGVK {
		panic("desired and actual must have same GVK")
	}

	// Get OpenAPISchema to have the correct merge and field configuration.
	s, err := d.openAPIAccessor.Get(desiredGVK.GroupVersion())
	if err != nil {
		return res, fmt.Errorf("API accessor: %w", err)
	}

	// If there is a Status subresource defined, add .status to stripset to ignore it.
	localStripSet := stripSet

	if hasStatusSubresource(s) {
		var paths []fieldpath.Path

		stripSet.Iterate(func(p fieldpath.Path) {
			paths = append(paths, p)
		})

		paths = append(paths, fieldpath.MakePathOrDie("status"))
		localStripSet = fieldpath.NewSet(paths...)
	}

	ss, err := schemaconv.ToSchemaFromOpenAPI(s.Components.Schemas, false)
	if err != nil {
		return res, fmt.Errorf("schema from OpenAPI: %w", err)
	}

	var parser typed.Parser

	ss.CopyInto(&parser.Schema)

	// Extrapolate a field set from desired.
	desiredObject = desiredObject.DeepCopyObject().(Object)
	if options.Owner != nil {
		if err := options.OwnerStrategy.SetControllerReference(
			options.Owner, desiredObject,
		); err != nil {
			return res, err
		}
	}

	tName, err := openAPICanonicalName(desiredObject)
	if err != nil {
		return res, err
	}

	typedDesired, err := getTyped(&parser, tName, desiredObject)
	if err != nil {
		return res, fmt.Errorf("desired object: %w", err)
	}

	res.DesiredFieldSet, err = typedDesired.ToFieldSet()
	if err != nil {
		return res, fmt.Errorf("desired to field set: %w", err)
	}

	res.DesiredFieldSet = res.DesiredFieldSet.Difference(localStripSet)

	// Get "our" managed fields on actual.
	mf, ok := findManagedFields(d.fieldOwner, actualObject)
	if !ok {
		// not a single managed field from "us" -> diverged for sure
		for _, mf := range actualObject.GetManagedFields() {
			fs := &fieldpath.Set{}
			if err := fs.FromJSON(bytes.NewReader(mf.FieldsV1.Raw)); err != nil {
				return res, fmt.Errorf("field set for actual: %w", err)
			}

			fs = res.DesiredFieldSet.Intersection(fs)
			if fs.Empty() {
				continue
			}

			res.ConflictingMangers = append(res.ConflictingMangers, CompareResultManagedFields{
				Manager: mf.Manager,
				Fields:  fs,
			})
		}

		return res, nil
	}

	actualFieldSet := &fieldpath.Set{}
	if err := actualFieldSet.FromJSON(bytes.NewReader(mf.FieldsV1.Raw)); err != nil {
		return res, fmt.Errorf("field set for actual: %w", err)
	}

	// Diff field sets to get exclude all ownership references
	// that are the same between actual and desired.
	// Also limit results to leave nodes to keep resulting diff small.
	diff := res.DesiredFieldSet.Difference(actualFieldSet).Leaves()

	// Index diff into something more useful for the caller.
	for _, mf := range actualObject.GetManagedFields() {
		if mf.Manager == d.fieldOwner {
			continue
		}

		fs := &fieldpath.Set{}
		if err := fs.FromJSON(bytes.NewReader(mf.FieldsV1.Raw)); err != nil {
			return res, fmt.Errorf("field set for actual: %w", err)
		}

		c := CompareResultManagedFields{
			Manager: mf.Manager,
			Fields:  fs.Intersection(diff),
		}
		if !c.Fields.Empty() {
			res.ConflictingMangers = append(res.ConflictingMangers, c)
		}

		o := CompareResultManagedFields{
			Manager: mf.Manager,
			Fields:  fs.Difference(diff),
		}
		if o.Fields.Empty() {
			continue
		}

		res.OtherManagers = append(res.OtherManagers, o)
	}

	typedActual, err := getTyped(&parser, tName, actualObject)
	if err != nil {
		return res, fmt.Errorf("actual object: %w", err)
	}

	actualValues := typedActual.RemoveItems(localStripSet)

	res.Comparison, err = typedDesired.RemoveItems(localStripSet).Compare(actualValues)
	if err != nil {
		return res, fmt.Errorf("compare: %w", err)
	}

	return res, nil
}

func getTyped(
	parser *typed.Parser,
	typeName string, obj Object,
) (
	typedDesired *typed.TypedValue, err error,
) {
	switch tobj := obj.(type) {
	case *unstructured.Unstructured:
		typedDesired, err = parser.Type(typeName).FromUnstructured(tobj.Object)
		if err != nil {
			return typedDesired, fmt.Errorf("from unstructured: %w", err)
		}

	default:
		typedDesired, err = parser.Type(typeName).FromStructured(tobj)
		if err != nil {
			return typedDesired, fmt.Errorf("from structured: %w", err)
		}
	}

	return typedDesired, nil
}

// Returns the ManagedFields associated with the given field owner.
func findManagedFields(fieldOwner string, accessor metav1.Object) (metav1.ManagedFieldsEntry, bool) {
	objManagedFields := accessor.GetManagedFields()
	for _, mf := range objManagedFields {
		if mf.Manager == fieldOwner &&
			mf.Operation == metav1.ManagedFieldsOperationApply &&
			mf.Subresource == "" {
			return mf, true
		}
	}

	return metav1.ManagedFieldsEntry{}, false
}

// taken from:
// https://github.com/kubernetes/apimachinery/blob/v0.32.0-alpha.0/pkg/util/managedfields/internal/stripmeta.go#L39-L52
var stripSet = fieldpath.NewSet(
	fieldpath.MakePathOrDie("apiVersion"),
	fieldpath.MakePathOrDie("kind"),
	// When we use this stip set via RemoveItems(),
	// we don't want to remove everything under the metadata key.
	// fieldpath.MakePathOrDie("metadata"),
	fieldpath.MakePathOrDie("metadata", "name"),
	fieldpath.MakePathOrDie("metadata", "namespace"),
	fieldpath.MakePathOrDie("metadata", "creationTimestamp"),
	fieldpath.MakePathOrDie("metadata", "selfLink"),
	fieldpath.MakePathOrDie("metadata", "uid"),
	fieldpath.MakePathOrDie("metadata", "clusterName"),
	fieldpath.MakePathOrDie("metadata", "generation"),
	fieldpath.MakePathOrDie("metadata", "managedFields"),
	fieldpath.MakePathOrDie("metadata", "resourceVersion"),
)

var existingAPIScheme = runtime.NewScheme()

func init() {
	schemeBuilder := runtime.SchemeBuilder{
		scheme.AddToScheme,
		apiextensionsv1.AddToScheme,
		apiextensions.AddToScheme,
	}
	if err := schemeBuilder.AddToScheme(existingAPIScheme); err != nil {
		panic(err)
	}
}

// Returns the canonical name to find the OpenAPISchema for the given objects GVK.
func openAPICanonicalName(obj client.Object) (string, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()

	var schemaTypeName string

	o, err := existingAPIScheme.New(gvk)

	switch {
	case err != nil && runtime.IsNotRegisteredError(err):
		// Assume CRD, when GVK is not part of core APIs.
		schemaTypeName = fmt.Sprintf("%s/%s.%s", gvk.Group, gvk.Version, gvk.Kind)
	case err != nil:
		return "", err
	default:
		return util.GetCanonicalTypeName(o), nil
	}

	return util.ToRESTFriendlyName(schemaTypeName), nil
}

func ensureGVKIsSet(obj client.Object, scheme *runtime.Scheme) error {
	if !obj.GetObjectKind().GroupVersionKind().Empty() {
		return nil
	}

	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return err
	}

	obj.GetObjectKind().SetGroupVersionKind(gvk)

	return nil
}

const statusSubresourceSuffix = "{name}/status"

// Determines if the schema has a Status subresource defined.
// If so the Comparator has to ignore .status, because the API server will also ignore these fields.
func hasStatusSubresource(openAPISchema *spec3.OpenAPI) bool {
	if openAPISchema.Paths == nil {
		return false
	}

	for path := range openAPISchema.Paths.Paths {
		if strings.HasSuffix(path, statusSubresourceSuffix) {
			return true
		}
	}

	return false
}
