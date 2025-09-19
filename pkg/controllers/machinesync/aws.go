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
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ensureAWSLoadBalancerMatch validates that when converting from MAPI->CAPI:
//   - Control plane machines must define an internal load balancer with "-int" suffix.
//   - Control plane machines can define an secondary external load balancer with "-ext" suffix.
//   - MAPI machine's load balancers must match the AWSCluster load balancers.
//   - Worker machines must not define load balancers.
func (r *MachineSyncReconciler) ensureAWSLoadBalancerMatch(ctx context.Context, mapiMachine *machinev1beta1.Machine) error {
	providerSpec, err := mapi2capi.AWSProviderSpecFromRawExtension(mapiMachine.Spec.ProviderSpec.Value)
	if err != nil {
		return fmt.Errorf("unable to parse Machine API providerSpec: %w", err)
	}

	lbfieldPath := field.NewPath("spec", "providerSpec", "value", "loadBalancers")

	if !util.IsControlPlaneMAPIMachine(mapiMachine) {
		if len(providerSpec.LoadBalancers) == 0 {
			return nil
		}

		return field.ErrorList{field.Invalid(lbfieldPath, providerSpec.LoadBalancers, "loadBalancers are not supported for worker machines")}.
			ToAggregate()
	}

	awsCluster := &awsv1.AWSCluster{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: r.Infra.Status.InfrastructureName}, awsCluster); err != nil {
		return fmt.Errorf("failed to get AWSCluster: %w", err)
	}

	if awsCluster.Spec.ControlPlaneLoadBalancer == nil || awsCluster.Spec.ControlPlaneLoadBalancer.Name == nil || *awsCluster.Spec.ControlPlaneLoadBalancer.Name == "" {
		return field.ErrorList{field.Invalid(lbfieldPath, providerSpec.LoadBalancers, "no control plane load balancer configured on AWSCluster")}.
			ToAggregate()
	}

	loadBalancersCopy := map[string]machinev1beta1.AWSLoadBalancerType{}
	for _, lb := range providerSpec.LoadBalancers {
		loadBalancersCopy[lb.Name] = lb.Type
	}

	errs := ensureExpectedLoadBalancer(lbfieldPath, &providerSpec, loadBalancersCopy, awsCluster.Spec.ControlPlaneLoadBalancer)

	if awsCluster.Spec.SecondaryControlPlaneLoadBalancer != nil {
		errs = append(errs, ensureExpectedLoadBalancer(lbfieldPath, &providerSpec, loadBalancersCopy, awsCluster.Spec.SecondaryControlPlaneLoadBalancer)...)
	}

	errs = append(errs, ensureNoRemainingLoadBalancers(lbfieldPath, &providerSpec, loadBalancersCopy)...)

	if len(errs) > 0 {
		return errs.ToAggregate()
	}

	return nil
}

// ensureNoRemainingLoadBalancers validates that there are no unexpected load balancers left defined on the machine.
func ensureNoRemainingLoadBalancers(
	lbfieldPath *field.Path,
	providerConfig *machinev1beta1.AWSMachineProviderConfig,
	remainingLoadBalancers map[string]machinev1beta1.AWSLoadBalancerType,
) field.ErrorList {
	// Everything in remainingLoadBalancers should be empty
	errList := field.ErrorList{}
	if len(remainingLoadBalancers) == 0 {
		return errList
	}

	unexpectedNames := make([]string, 0, len(remainingLoadBalancers))
	for name := range remainingLoadBalancers {
		unexpectedNames = append(unexpectedNames, name)
	}

	sort.Strings(unexpectedNames)

	for _, name := range unexpectedNames {
		errList = append(errList, field.Invalid(lbfieldPath, providerConfig.LoadBalancers, fmt.Sprintf("unexpected load balancer %q defined on machine", name)))
	}

	return errList
}

// ensureExpectedLoadBalancer validates that remainingLoadBalancers contains the expected load balancer.
// If the expected load balancer is found, it is removed from remainingLoadBalancers.
func ensureExpectedLoadBalancer(
	lbfieldPath *field.Path,
	providerConfig *machinev1beta1.AWSMachineProviderConfig,
	remainingLoadBalancers map[string]machinev1beta1.AWSLoadBalancerType,
	expectedLoadBalancer *awsv1.AWSLoadBalancerSpec,
) field.ErrorList {
	if expectedLoadBalancer == nil {
		return field.ErrorList{}
	}

	expectedLBName := ptr.Deref(expectedLoadBalancer.Name, "")
	expectedLBType := convertAWSLBTypeToMAPI(expectedLoadBalancer.LoadBalancerType)

	errList := field.ErrorList{}
	if t, found := remainingLoadBalancers[expectedLBName]; !found {
		errList = append(errList, field.Invalid(lbfieldPath, providerConfig.LoadBalancers, fmt.Sprintf("must include load balancer named %q", expectedLBName)))
	} else if t != expectedLBType {
		errList = append(errList, field.Invalid(lbfieldPath, providerConfig.LoadBalancers, fmt.Sprintf("load balancer %q must be of type %q to match AWSCluster", expectedLBName, expectedLBType)))
	}

	delete(remainingLoadBalancers, expectedLBName)

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

// ensurePlatformMAPIToCAPIValidations verifies that shared CAPI resources are compatible before converting from MAPI -> CAPI.
func (r *MachineSyncReconciler) ensurePlatformMAPIToCAPIValidations(ctx context.Context, mapiMachine *machinev1beta1.Machine) error {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return r.ensureAWSLoadBalancerMatch(ctx, mapiMachine)
	default:
		return nil
	}
}
