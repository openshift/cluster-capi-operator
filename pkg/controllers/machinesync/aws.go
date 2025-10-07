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

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validateMachineAWSLoadBalancers validates that when converting from MAPI->CAPI:
//   - Control plane machines must define an internal load balancer with "-int" suffix.
//   - Control plane machines can define an secondary external load balancer with "-ext" suffix.
//   - MAPI machine's load balancers must match the AWSCluster load balancers.
//   - Worker machines must not define load balancers.
func (r *MachineSyncReconciler) validateMachineAWSLoadBalancers(ctx context.Context, mapiMachine *mapiv1beta1.Machine) error {
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

	expectedLoadBalancers := map[string]mapiv1beta1.AWSLoadBalancerType{
		ptr.Deref(awsCluster.Spec.ControlPlaneLoadBalancer.Name, ""): convertAWSLBTypeToMAPI(awsCluster.Spec.ControlPlaneLoadBalancer.LoadBalancerType),
	}

	if awsCluster.Spec.SecondaryControlPlaneLoadBalancer != nil {
		if awsCluster.Spec.SecondaryControlPlaneLoadBalancer.Name == nil || *awsCluster.Spec.SecondaryControlPlaneLoadBalancer.Name == "" {
			return field.ErrorList{field.Invalid(lbfieldPath, providerSpec.LoadBalancers, "secondary control plane load balancer name is not configured on AWSCluster")}.
				ToAggregate()
		}

		expectedLoadBalancers[ptr.Deref(awsCluster.Spec.SecondaryControlPlaneLoadBalancer.Name, "")] = convertAWSLBTypeToMAPI(awsCluster.Spec.SecondaryControlPlaneLoadBalancer.LoadBalancerType)
	}

	return validateLoadBalancerReferencesAgainstExpected(providerSpec.LoadBalancers, expectedLoadBalancers, lbfieldPath)
}

// validateLoadBalancerReferencesAgainstExpected validates that the actual load balancers match the expected load balancers.
func validateLoadBalancerReferencesAgainstExpected(
	actualLoadBalancers []mapiv1beta1.LoadBalancerReference,
	expectedLoadBalancers map[string]mapiv1beta1.AWSLoadBalancerType,
	lbfieldPath *field.Path,
) error {
	errs := field.ErrorList{}
	foundLoadBalancers := map[string]bool{}

	for i, lb := range actualLoadBalancers {
		indexPath := lbfieldPath.Index(i)

		expectedType, isExpected := expectedLoadBalancers[lb.Name]
		if !isExpected {
			errs = append(errs, field.Invalid(indexPath.Child("name"), lb.Name, fmt.Sprintf("unexpected load balancer %q defined on machine", lb.Name)))
			continue
		}

		if lb.Type != expectedType {
			errs = append(errs, field.Invalid(indexPath.Child("type"), lb.Type, fmt.Sprintf("load balancer %q must be of type %q to match AWSCluster", lb.Name, expectedType)))
		}

		foundLoadBalancers[lb.Name] = true
	}

	for expectedName := range expectedLoadBalancers {
		if !foundLoadBalancers[expectedName] {
			errs = append(errs, field.Invalid(lbfieldPath, actualLoadBalancers, fmt.Sprintf("must include load balancer named %q", expectedName)))
		}
	}

	return errs.ToAggregate()
}

// convertAWSLBTypeToMAPI converts CAPI LoadBalancerType to MAPI AWSLoadBalancerType.
func convertAWSLBTypeToMAPI(capiType awsv1.LoadBalancerType) mapiv1beta1.AWSLoadBalancerType {
	switch capiType {
	case awsv1.LoadBalancerTypeClassic, awsv1.LoadBalancerTypeELB, "":
		return mapiv1beta1.ClassicLoadBalancerType
	case awsv1.LoadBalancerTypeNLB:
		return mapiv1beta1.NetworkLoadBalancerType
	default:
		return mapiv1beta1.ClassicLoadBalancerType
	}
}
