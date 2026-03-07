// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/yaml"

	bmov1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	infrastructureName = "cluster"
	infraAPIGroup      = "infrastructure.cluster.x-k8s.io"
)

var (
	cl          client.Client
	ctx         = context.Background()
	platform    configv1.PlatformType
	clusterName string

	// resourcesUnderTest tracks objects created by the current test for focused
	// diagnostics on failure. Helpers call trackResource after creating objects;
	// ReportAfterEach dumps detailed state for each tracked resource then clears
	// the list.
	resourcesUnderTest []client.Object

	// specDiagnostics stores per-spec diagnostic output keyed by spec text.
	// ReportAfterEach populates it on failure; ReportAfterSuite reads it to
	// append diagnostics to the JUnit failure message.
	specDiagnostics = map[string]string{}
)

func init() {
	utilruntime.Must(configv1.Install(scheme.Scheme))
	utilruntime.Must(awsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(gcpv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(azurev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(mapiv1beta1.AddToScheme(scheme.Scheme))
	utilruntime.Must(ibmpowervsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(openstackv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(vspherev1.AddToScheme(scheme.Scheme))
	utilruntime.Must(metal3v1.AddToScheme(scheme.Scheme))
	utilruntime.Must(bmov1alpha1.AddToScheme(scheme.Scheme))
}

// InitCommonVariables initializes global variables used across test cases.
func InitCommonVariables() {
	logf.SetLogger(GinkgoLogr)
	ctrl.SetLogger(GinkgoLogr)

	cfg, err := config.GetConfig()
	Expect(err).ToNot(HaveOccurred())

	cl, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())

	infra := &configv1.Infrastructure{}
	infraName := client.ObjectKey{
		Name: infrastructureName,
	}
	Expect(cl.Get(ctx, infraName, infra)).To(Succeed())
	Expect(infra.Status.PlatformStatus).ToNot(BeNil())
	clusterName = infra.Status.InfrastructureName
	platform = infra.Status.PlatformStatus.Type

	komega.SetClient(cl)
	komega.SetContext(ctx)
}

// trackResource registers a resource for focused diagnostics on test failure.
// The object must have Name and Namespace set. The object's type is used to
// determine how to fetch and format it in the failure dump.
func trackResource(obj client.Object) {
	resourcesUnderTest = append(resourcesUnderTest, obj)
}

// collectTrackedResourceDiagnostics builds a diagnostics string for each
// tracked resource. For each resource it fetches current state, marshals it as
// YAML, and lists events specific to that object. It also dumps all
// AWSMachineTemplates (on AWS) and all events in both namespaces.
// Best-effort: panics are recovered and individual errors are logged without
// aborting the dump.
func collectTrackedResourceDiagnostics() string {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "WARNING: collectTrackedResourceDiagnostics panicked: %v\n", r)
		}
	}()

	var buf strings.Builder
	buf.WriteString("\n=== Test Failure Diagnostics ===\n")

	for _, obj := range resourcesUnderTest {
		dumpSingleResource(&buf, obj)
	}

	if platform == configv1.AWSPlatformType {
		dumpAllAWSMachineTemplates(&buf)
	}
	dumpNamespaceEvents(&buf, capiframework.CAPINamespace)
	dumpNamespaceEvents(&buf, capiframework.MAPINamespace)

	buf.WriteString("\n=== End Test Failure Diagnostics ===\n")

	return buf.String()
}

func dumpSingleResource(buf *strings.Builder, obj client.Object) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(buf, "\n--- (panic dumping %T): %v ---\n", obj, r)
		}
	}()

	key := client.ObjectKeyFromObject(obj)
	typeName := reflect.TypeOf(obj).Elem().Name()

	// Create a fresh instance of the same type to Get into.
	fresh := reflect.New(reflect.TypeOf(obj).Elem()).Interface().(client.Object)

	if err := cl.Get(ctx, key, fresh); err != nil {
		if apierrors.IsNotFound(err) {
			fmt.Fprintf(buf, "\n--- %s %s/%s: not found (deleted) ---\n", typeName, key.Namespace, key.Name)
		} else {
			fmt.Fprintf(buf, "\n--- %s %s/%s: error fetching: %v ---\n", typeName, key.Namespace, key.Name, err)
		}

		return
	}

	fmt.Fprintf(buf, "\n--- %s %s/%s ---\n", typeName, key.Namespace, key.Name)
	describeObject(buf, fresh)
	describeObjectEvents(buf, key)
}

func describeObject(buf *strings.Builder, obj client.Object) {
	obj.SetManagedFields(nil)

	annotations := obj.GetAnnotations()
	if annotations != nil {
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		obj.SetAnnotations(annotations)
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		fmt.Fprintf(buf, "  (failed to marshal %T: %v)\n", obj, err)
		return
	}

	buf.Write(out)
}

// describeObjectEvents lists events for the given object. Matching is by name
// only; events for different resource kinds with the same name in the same
// namespace will be included.
func describeObjectEvents(buf *strings.Builder, key client.ObjectKey) {
	list := &corev1.EventList{}
	if err := cl.List(ctx, list, client.InNamespace(key.Namespace)); err != nil {
		fmt.Fprintf(buf, "  Events: error listing: %v\n", err)
		return
	}

	var matching []corev1.Event

	for i := range list.Items {
		if list.Items[i].InvolvedObject.Name == key.Name {
			matching = append(matching, list.Items[i])
		}
	}

	if len(matching) == 0 {
		fmt.Fprintf(buf, "  Events: none\n")
		return
	}

	fmt.Fprintf(buf, "  Events:\n")

	for i := range matching {
		e := &matching[i]
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.EventTime.Time
		}

		fmt.Fprintf(buf, "    %s %-8s %-25s %s\n",
			ts.Format(time.RFC3339), e.Type, e.Reason, e.Message)
	}
}

// dumpAllAWSMachineTemplates lists all AWSMachineTemplates in the CAPI namespace
// and describes each one. Templates use generated names so we list rather than
// trying to predict names.
func dumpAllAWSMachineTemplates(buf *strings.Builder) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(buf, "\n--- (panic dumping AWSMachineTemplates): %v ---\n", r)
		}
	}()

	list := &awsv1.AWSMachineTemplateList{}
	if err := cl.List(ctx, list, client.InNamespace(capiframework.CAPINamespace)); err != nil {
		fmt.Fprintf(buf, "\n--- AWSMachineTemplates: error listing: %v ---\n", err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n--- AWSMachineTemplates in %s (%d) ---\n", capiframework.CAPINamespace, len(list.Items))

	for i := range list.Items {
		t := &list.Items[i]
		key := client.ObjectKeyFromObject(t)
		fmt.Fprintf(buf, "\n  %s:\n", t.Name)
		describeObject(buf, t)
		describeObjectEvents(buf, key)
	}
}

// dumpNamespaceEvents lists all events in a namespace. This catches events not
// associated with tracked resources (e.g. from controllers acting on other objects).
func dumpNamespaceEvents(buf *strings.Builder, namespace string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(buf, "\n--- (panic dumping events in %s): %v ---\n", namespace, r)
		}
	}()

	list := &corev1.EventList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n--- Events in %s: error listing: %v ---\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n--- Events in %s (%d) ---\n", namespace, len(list.Items))

	for i := range list.Items {
		e := &list.Items[i]
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.EventTime.Time
		}

		fmt.Fprintf(buf, "  %s %-8s %s/%-30s %-25s %s\n",
			ts.Format(time.RFC3339), e.Type,
			e.InvolvedObject.Kind, e.InvolvedObject.Name,
			e.Reason, e.Message)
	}
}
