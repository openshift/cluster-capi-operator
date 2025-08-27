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

package machinesync

import (
	"context"
	"fmt"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	"k8s.io/apimachinery/pkg/util/validation/field"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ensureAWSLoadBalancerMatch enforces that when converting from MAPI->CAPI:
//   - Worker machines must not define load balancers.
//   - Control plane machines must define an internal load balancer with "-int" suffix.
//   - Control plane machines can define an secondary external load balancer with "-ext" suffix.
//   - MAPI machine's load balancers must match the AWSCluster load balancers.
func (r *MachineSyncReconciler) ensureAWSLoadBalancerMatch(ctx context.Context, mapiMachine *machinev1beta1.Machine) error {
	spec, err := mapi2capi.AWSProviderSpecFromRawExtension(mapiMachine.Spec.ProviderSpec.Value)
	if err != nil {
		return fmt.Errorf("unable to parse Machine API providerSpec: %w", err)
	}

	lbfieldPath := field.NewPath("spec", "providerSpec", "value", "loadBalancers")

	if !util.IsControlPlaneMAPIMachine(mapiMachine) {
		if len(spec.LoadBalancers) == 0 {
			return nil
		}
		return field.ErrorList{field.Invalid(lbfieldPath, spec.LoadBalancers, "loadBalancers are not supported for worker machines")}.
			ToAggregate()
	}

	awsCluster := &awsv1.AWSCluster{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: r.Infra.Status.InfrastructureName}, awsCluster); err != nil {
		return fmt.Errorf("failed to get AWSCluster: %w", err)
	}

	if awsCluster.Spec.ControlPlaneLoadBalancer == nil || awsCluster.Spec.ControlPlaneLoadBalancer.Name == nil || *awsCluster.Spec.ControlPlaneLoadBalancer.Name == "" {
		return field.ErrorList{field.Invalid(lbfieldPath, spec.LoadBalancers, "no control plane load balancer configured on AWSCluster")}.
			ToAggregate()
	}

	loadBalancers := map[string]machinev1beta1.AWSLoadBalancerType{}
	for _, lb := range spec.LoadBalancers {
		loadBalancers[lb.Name] = lb.Type
	}

	errs := ensureExpectedLoadBalancer(lbfieldPath, spec.LoadBalancers, loadBalancers, awsCluster.Spec.ControlPlaneLoadBalancer)

	if awsCluster.Spec.SecondaryControlPlaneLoadBalancer != nil {
		errs = append(errs, ensureExpectedLoadBalancer(lbfieldPath, spec.LoadBalancers, loadBalancers, awsCluster.Spec.SecondaryControlPlaneLoadBalancer)...)
	}

	errs = append(errs, ensureNoRemainingLoadBalancers(lbfieldPath, spec.LoadBalancers, loadBalancers)...)

	if len(errs) > 0 {
		return errs.ToAggregate()
	}

	return nil
}

// ensureNoRemainingLoadBalancers validates that there are no unexpected load balancers left defined on the machine.
func ensureNoRemainingLoadBalancers(lbfieldPath *field.Path, provided []machinev1beta1.LoadBalancerReference, providedByName map[string]machinev1beta1.AWSLoadBalancerType) field.ErrorList {
	// Everything in providedByName should be empty
	errList := field.ErrorList{}
	if len(providedByName) == 0 {
		return errList
	}

	unexpectedNames := make([]string, 0, len(providedByName))
	for name := range providedByName {
		unexpectedNames = append(unexpectedNames, name)
	}
	sort.Strings(unexpectedNames)

	for _, name := range unexpectedNames {
		errList = append(errList, field.Invalid(lbfieldPath, provided, fmt.Sprintf("unexpected load balancer %q defined on machine", name)))
	}
	return errList
}

// ensureExpectedLoadBalancer validates that the expected load balancer exists with the correct type
// and removes it from providedByName to avoid duplicate unexpected errors later.
func ensureExpectedLoadBalancer(
	lbfieldPath *field.Path,
	loadBalancers []machinev1beta1.LoadBalancerReference,
	loadBalancersMap map[string]machinev1beta1.AWSLoadBalancerType,
	expected *awsv1.AWSLoadBalancerSpec,
) field.ErrorList {

	expectedName := ""
	if expected.Name != nil {
		expectedName = *expected.Name
	}
	expectedType := convertAWSLBTypeToMAPI(expected.LoadBalancerType)

	errList := field.ErrorList{}
	if t, found := loadBalancersMap[expectedName]; !found {
		errList = append(errList, field.Invalid(lbfieldPath, loadBalancers, fmt.Sprintf("must include load balancer named %q", expectedName)))
	} else if t != expectedType {
		errList = append(errList, field.Invalid(lbfieldPath, loadBalancers, fmt.Sprintf("load balancer %q must be of type %q to match AWSCluster", expectedName, expectedType)))
	}

	delete(loadBalancersMap, expectedName)
	return errList
}

// convertAWSLBTypeToMAPI converts CAPI LoadBalancerType to MAPI AWSLoadBalancerType.
func convertAWSLBTypeToMAPI(capiType awsv1.LoadBalancerType) machinev1beta1.AWSLoadBalancerType {
	switch capiType {
	case awsv1.LoadBalancerTypeClassic, awsv1.LoadBalancerTypeELB, "":
		return machinev1beta1.ClassicLoadBalancerType
	case awsv1.LoadBalancerTypeNLB:
		return machinev1beta1.NetworkLoadBalancerType
	default:
		return machinev1beta1.ClassicLoadBalancerType
	}
}

// ensurePlatformMAPIToCAPIValidations performs platform-specific validations before converting a MAPI Machine to a CAPI Machine.
// This abstracts platform branching away from higher-level reconciliation logic.
func (r *MachineSyncReconciler) ensurePlatformMAPIToCAPIValidations(ctx context.Context, mapiMachine *machinev1beta1.Machine) error {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return r.ensureAWSLoadBalancerMatch(ctx, mapiMachine)
	default:
		return nil
	}
}
