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
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
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

	lbFieldPath := field.NewPath("spec", "providerSpec", "value", "loadBalancers")

	if !util.IsControlPlaneMAPIMachine(mapiMachine) {
		if len(providerSpec.LoadBalancers) == 0 {
			return nil
		}

		return field.Invalid(lbFieldPath, providerSpec.LoadBalancers, "loadBalancers are not supported for non-control plane machines")
	}

	awsCluster := &awsv1.AWSCluster{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: r.Infra.Status.InfrastructureName}, awsCluster); err != nil {
		return fmt.Errorf("failed to get AWSCluster: %w", err)
	}

	if awsCluster.Spec.ControlPlaneLoadBalancer == nil || ptr.Deref(awsCluster.Spec.ControlPlaneLoadBalancer.Name, "") == "" {
		return field.Invalid(lbFieldPath, providerSpec.LoadBalancers, "no control plane load balancer configured on AWSCluster")
	}

	controlPlaneLB, err := capi2mapi.ConvertAWSLoadBalancerToMAPI(awsCluster.Spec.ControlPlaneLoadBalancer)
	if err != nil {
		return field.Invalid(lbFieldPath, providerSpec.LoadBalancers, fmt.Sprintf("failed to convert control plane load balancer: %v", err))
	}

	expectedLoadBalancers := map[string]mapiv1beta1.AWSLoadBalancerType{
		ptr.Deref(awsCluster.Spec.ControlPlaneLoadBalancer.Name, ""): controlPlaneLB.Type,
	}

	if awsCluster.Spec.SecondaryControlPlaneLoadBalancer != nil {
		if ptr.Deref(awsCluster.Spec.SecondaryControlPlaneLoadBalancer.Name, "") == "" {
			return field.Invalid(lbFieldPath, providerSpec.LoadBalancers, "secondary control plane load balancer name is not configured on AWSCluster")
		}

		secondaryControlPlaneLB, err := capi2mapi.ConvertAWSLoadBalancerToMAPI(awsCluster.Spec.SecondaryControlPlaneLoadBalancer)
		if err != nil {
			return field.Invalid(lbFieldPath, providerSpec.LoadBalancers, fmt.Sprintf("failed to convert secondary control plane load balancer: %v", err))
		}

		expectedLoadBalancers[ptr.Deref(awsCluster.Spec.SecondaryControlPlaneLoadBalancer.Name, "")] = secondaryControlPlaneLB.Type
	}

	return validateLoadBalancerReferencesAgainstExpected(providerSpec.LoadBalancers, expectedLoadBalancers, lbFieldPath)
}

// validateLoadBalancerReferencesAgainstExpected validates that the actual load balancers match the expected load balancers.
func validateLoadBalancerReferencesAgainstExpected(
	actualLoadBalancers []mapiv1beta1.LoadBalancerReference,
	expectedLoadBalancers map[string]mapiv1beta1.AWSLoadBalancerType,
	lbFieldPath *field.Path,
) error {
	errs := field.ErrorList{}
	foundLoadBalancers := map[string]bool{}

	for i, lb := range actualLoadBalancers {
		indexPath := lbFieldPath.Index(i)

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
			errs = append(errs, field.Invalid(lbFieldPath, actualLoadBalancers, fmt.Sprintf("must include load balancer named %q", expectedName)))
		}
	}

	return errs.ToAggregate()
}
