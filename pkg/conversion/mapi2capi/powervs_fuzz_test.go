/*
Copyright 2024 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package mapi2capi_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	randfill "sigs.k8s.io/randfill"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	powerVSProviderSpecKind = "PowerVSMachineProviderConfig"
)

var _ = Describe("PowerVS Fuzz (mapi2capi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &ibmpowervsv1.IBMPowerVSCluster{
		Spec: ibmpowervsv1.IBMPowerVSClusterSpec{
			ServiceInstance: &ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("serviceInstance")},
			Zone:            ptr.To("test-zone"),
		},
	}

	Context("PowerVSMachine Conversion", func() {
		fromMachineAndPowerVSMachineAndPowerVSCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			powerVSMachine, ok := infraMachine.(*ibmpowervsv1.IBMPowerVSMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSMachine{}, infraMachine)

			powerVSCluster, ok := infraCluster.(*ibmpowervsv1.IBMPowerVSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSCluster{}, infraCluster)

			return capi2mapi.FromMachineAndPowerVSMachineAndPowerVSCluster(machine, powerVSMachine, powerVSCluster)
		}

		conversiontest.MAPI2CAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromPowerVSMachineAndInfra,
			fromMachineAndPowerVSMachineAndPowerVSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1.PowerVSMachineProviderConfig{}, powerVSProviderIDFuzzer),
			powerVSProviderSpecFuzzerFuncs,
		)
	})

	Context("PowerVSMachineSet Conversion", func() {
		fromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			powerVSMachineTemplate, ok := infraMachineTemplate.(*ibmpowervsv1.IBMPowerVSMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSMachineTemplate{}, infraMachineTemplate)

			powerVSCluster, ok := infraCluster.(*ibmpowervsv1.IBMPowerVSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster(machineSet, powerVSMachineTemplate, powerVSCluster)
		}

		conversiontest.MAPI2CAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromPowerVSMachineSetAndInfra,
			fromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1.PowerVSMachineProviderConfig{}, powerVSProviderIDFuzzer),
			conversiontest.MAPIMachineSetFuzzerFuncs(),
			powerVSProviderSpecFuzzerFuncs,
		)
	})
})

func powerVSProviderIDFuzzer(c randfill.Continue) string {
	// Power VS provider id format: ibmpowervs://<region>/<zone>/<service_instance_id>/<instance_id>
	return fmt.Sprintf("ibmpowervs://tok/tok04/%s/%s", strings.ReplaceAll(c.String(0), "/", ""), strings.ReplaceAll(c.String(0), "/", ""))
}

//nolint:funlen
func powerVSProviderSpecFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(serviceInstance *mapiv1.PowerVSResource, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				*serviceInstance = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeID,
					ID:   ptr.To(c.String(0)),
				}
			case 1:
				*serviceInstance = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeID,
					ID:   ptr.To(c.String(0)),
				}
			case 2:
				*serviceInstance = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeID,
					ID:   ptr.To(c.String(0)),
				}
			}
		},
		func(image *mapiv1.PowerVSResource, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				*image = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeID,
					ID:   ptr.To(c.String(0)),
				}
			case 1:
				*image = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeName,
					Name: ptr.To(c.String(0)),
				}
			case 2:
				*image = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeRegEx,
					Name: ptr.To(c.String(0)),
				}
			}
		},
		func(network *mapiv1.PowerVSResource, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				*network = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeID,
					ID:   ptr.To(c.String(0)),
				}
			case 1:
				*network = mapiv1.PowerVSResource{
					Type: mapiv1.PowerVSResourceTypeName,
					Name: ptr.To(c.String(0)),
				}
			case 2:
				*network = mapiv1.PowerVSResource{
					Type:  mapiv1.PowerVSResourceTypeRegEx,
					RegEx: ptr.To(c.String(0)),
				}
			}
		},
		func(pc *mapiv1.PowerVSMachineProviderConfig, c randfill.Continue) {
			c.FillNoCustom(pc)

			// The type meta is always set to these values by the conversion.
			pc.APIVersion = mapiv1.SchemeGroupVersion.String()
			pc.Kind = powerVSProviderSpecKind

			pc.LoadBalancers = nil
			pc.ObjectMeta = metav1.ObjectMeta{}
			pc.CredentialsSecret = nil

			// Clear pointers to empty structs.
			if pc.UserDataSecret != nil && pc.UserDataSecret.Name == "" {
				pc.UserDataSecret = nil
			}
		},
	}
}
