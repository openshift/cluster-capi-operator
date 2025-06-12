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
package capi2mapi_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	kubescheme "k8s.io/client-go/kubernetes/scheme"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var scheme *runtime.Scheme

func init() {
	// Register the scheme for the test.
	// This must be done before the tests are run as the fuzzer is needed before the test tree is compiled.
	scheme = kubescheme.Scheme
	if err := clusterv1.AddToScheme(scheme); err != nil {
		panic(fmt.Sprintf("failed to add cluster API scheme: %v", err))
	}

	if err := awsv1.AddToScheme(scheme); err != nil {
		panic(fmt.Sprintf("failed to add AWS scheme: %v", err))
	}

	if err := openstackv1.AddToScheme(scheme); err != nil {
		panic(fmt.Sprintf("failed to add OpenStack scheme: %v", err))
	}
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CAPI2MAPI Suite")
}
