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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

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

	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
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

// dumpClusterState logs Machines, MachineSets, and Events from both MAPI and CAPI
// namespaces. Called on test failure to capture resource state before cleanup removes them.
func dumpClusterState() {
	namespaces := []string{capiframework.MAPINamespace, capiframework.CAPINamespace}

	var buf strings.Builder
	buf.WriteString("\n=== Cluster State Dump (test failure) ===\n")

	for _, ns := range namespaces {
		dumpMAPIMachines(&buf, ns)
		dumpCAPIMachines(&buf, ns)
		dumpMAPIMachineSets(&buf, ns)
		dumpCAPIMachineSets(&buf, ns)
		dumpEvents(&buf, ns)
	}

	if platform == configv1.AWSPlatformType {
		for _, ns := range namespaces {
			dumpAWSMachines(&buf, ns)
			dumpAWSMachineTemplates(&buf, ns)
		}
	}

	buf.WriteString("=== End Cluster State Dump ===\n")

	GinkgoWriter.Print(buf.String())
	AddReportEntry("cluster-state-dump", buf.String())
}

func dumpMAPIMachines(buf *strings.Builder, namespace string) {
	list := &mapiv1beta1.MachineList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] MAPI Machines: error listing: %v\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] MAPI Machines (%d):\n", namespace, len(list.Items))

	for i := range list.Items {
		m := &list.Items[i]
		phase := ptr.Deref(m.Status.Phase, "")
		fmt.Fprintf(buf, "  %-50s phase=%-12s authAPI=%-12s conditions=%s created=%s\n",
			m.Name, phase, m.Status.AuthoritativeAPI,
			summarizeMAPIConditions(m.Status.Conditions), m.CreationTimestamp.Format(time.RFC3339))
	}
}

func dumpCAPIMachines(buf *strings.Builder, namespace string) {
	list := &clusterv1.MachineList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] CAPI Machines: error listing: %v\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] CAPI Machines (%d):\n", namespace, len(list.Items))

	for i := range list.Items {
		m := &list.Items[i]
		fmt.Fprintf(buf, "  %-50s phase=%-12s conditions=%s created=%s\n",
			m.Name, m.Status.Phase,
			summarizeV1Beta2Conditions(m.Status.Conditions), m.CreationTimestamp.Format(time.RFC3339))
	}
}

func dumpMAPIMachineSets(buf *strings.Builder, namespace string) {
	list := &mapiv1beta1.MachineSetList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] MAPI MachineSets: error listing: %v\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] MAPI MachineSets (%d):\n", namespace, len(list.Items))

	for i := range list.Items {
		ms := &list.Items[i]
		replicas := ptr.Deref(ms.Spec.Replicas, 0)
		fmt.Fprintf(buf, "  %-50s replicas=%d/%d authAPI=%-12s conditions=%s\n",
			ms.Name, ms.Status.ReadyReplicas, replicas, ms.Status.AuthoritativeAPI,
			summarizeMAPIConditions(ms.Status.Conditions))
	}
}

func dumpCAPIMachineSets(buf *strings.Builder, namespace string) {
	list := &clusterv1.MachineSetList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] CAPI MachineSets: error listing: %v\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] CAPI MachineSets (%d):\n", namespace, len(list.Items))

	for i := range list.Items {
		ms := &list.Items[i]
		replicas := ptr.Deref(ms.Spec.Replicas, 0)
		fmt.Fprintf(buf, "  %-50s replicas=%d/%d conditions=%s\n",
			ms.Name, ptr.Deref(ms.Status.ReadyReplicas, 0), replicas,
			summarizeV1Beta2Conditions(ms.Status.Conditions))
	}
}

func dumpAWSMachines(buf *strings.Builder, namespace string) {
	list := &awsv1.AWSMachineList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] AWSMachines: error listing: %v\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] AWSMachines (%d):\n", namespace, len(list.Items))

	for i := range list.Items {
		m := &list.Items[i]
		providerID := ptr.Deref(m.Spec.ProviderID, "")
		instanceID := ptr.Deref(m.Spec.InstanceID, "")
		fmt.Fprintf(buf, "  %-50s instanceType=%-12s instanceID=%-22s providerID=%s created=%s\n",
			m.Name, m.Spec.InstanceType, instanceID, providerID, m.CreationTimestamp.Format(time.RFC3339))
	}
}

func dumpAWSMachineTemplates(buf *strings.Builder, namespace string) {
	list := &awsv1.AWSMachineTemplateList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] AWSMachineTemplates: error listing: %v\n", namespace, err)
		return
	}

	if len(list.Items) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] AWSMachineTemplates (%d):\n", namespace, len(list.Items))

	for i := range list.Items {
		t := &list.Items[i]
		fmt.Fprintf(buf, "  %-50s instanceType=%-12s created=%s\n",
			t.Name, t.Spec.Template.Spec.InstanceType, t.CreationTimestamp.Format(time.RFC3339))
	}
}

func dumpEvents(buf *strings.Builder, namespace string) {
	cutoff := time.Now().Add(-10 * time.Minute)

	list := &corev1.EventList{}
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		fmt.Fprintf(buf, "\n[%s] Events: error listing: %v\n", namespace, err)
		return
	}

	var recent []corev1.Event

	for i := range list.Items {
		e := &list.Items[i]
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.EventTime.Time
		}

		if ts.After(cutoff) {
			recent = append(recent, *e)
		}
	}

	if len(recent) == 0 {
		return
	}

	fmt.Fprintf(buf, "\n[%s] Events (last 10min, %d):\n", namespace, len(recent))

	for i := range recent {
		e := &recent[i]
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.EventTime.Time
		}

		fmt.Fprintf(buf, "  %s %s/%s %-8s %-20s %s\n",
			ts.Format(time.RFC3339),
			e.InvolvedObject.Kind, e.InvolvedObject.Name,
			e.Type, e.Reason, truncate(e.Message, 120))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return s[:max-3] + "..."
}

