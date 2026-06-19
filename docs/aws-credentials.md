# AWS CredentialsRequest

## Structure

The `openshift-cluster-api-aws` CredentialsRequest in `manifests/0000_30_cluster-api_01_credentials-request.yaml` instructs the Cloud Credential Operator (CCO) to mint AWS IAM credentials for the CAPA provider. It is gated on both `ClusterAPIMachineManagement` and `ClusterAPIMachineManagementAWS` feature gates.

## Permission Categories

| Category | Permissions | Rationale |
|----------|------------|-----------|
| Core EC2 | `RunInstances`, `TerminateInstances`, `DescribeInstances`, `CreateTags`, `DescribeImages`, `DescribeInstanceTypes` | Instance lifecycle called on every machine create/reconcile/delete |
| Network (unmanaged VPC) | `DescribeVpcs`, `DescribeSubnets`, `DescribeSecurityGroups`, `DescribeDhcpOptions`, `DescribeNetworkInterfaces`, `DescribeNetworkInterfaceAttribute`, `ModifyNetworkInterfaceAttribute` | Resolving VPC, subnet, and security group references for instance placement. Used even in unmanaged VPC mode. |
| Dedicated hosts | `AllocateHosts`, `ReleaseHosts` | Conditional, required when `AWSMachine.Spec.DynamicHostAllocationSpec` is set for dedicated tenancy |
| ELB | `RegisterTargets`, `DeregisterTargets`, `RegisterInstancesWithLoadBalancer`, `DescribeTargetGroups`, `DescribeLoadBalancers`, `DescribeTargetHealth` | Conditional, gated by `IsControlPlane()`. Required for control plane machine LB registration. |
| IAM | `PassRole` | Implicit, EC2 checks this when `RunInstances` specifies an `IamInstanceProfile` |
| KMS | `Decrypt`, `Encrypt`, `GenerateDataKey`, `GenerateDataKeyWithoutPlainText`, `DescribeKey`, `CreateGrant`, `RevokeGrant`, `ListGrants` | Conditional, required when machines use customer-managed CMK encryption for EBS volumes |

## Updating Permissions

Use the `/credentials-request-audit` skill to audit permissions for any provider. The process requires:

1. Clone the provider's OpenShift fork (e.g., `openshift/cluster-api-provider-aws`). The vendored code here only has API types.
2. Trace SDK calls from controller entry points, filtering for OpenShift's execution context (unmanaged infrastructure, externally-managed cluster resources).
3. Validate with SDK logs from an E2E test covering create, reconcile, and delete.
4. Classify as Active (keep), Conditional (keep — customer feature), or Unreachable (remove candidate).


