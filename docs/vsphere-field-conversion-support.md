# vSphere unsupported-field validation

The vSphere admission policy targets CAPV `v1beta1` `VSphereMachine` and
`VSphereMachineTemplate` resources in `openshift-cluster-api`. MachineTemplate
validations apply to `spec.template.spec`; Machine validations apply to `spec`.

The following settings have no supported Machine API conversion and are rejected
both by the deny policy and by Cluster API to Machine API conversion:

| Field | Accepted value |
| --- | --- |
| `nestedHV` | Omitted (explicit `true` and `false` are rejected) |
| `ftEncryptionMode` | Omitted or empty |
| `migrateEncryption` | Omitted or empty |
| `cryptoKeyID` | Omitted or empty |
| `cryptoProfile` | Omitted or empty |
| `storagePolicyName` | Omitted or empty |
| `hardwareVersion` | Omitted or empty |
| `pciDevices` | Omitted or empty list |

These virtualization and encryption fields already exist in CAPV `v1beta1`.
Conversion must not silently drop configured settings, including requirements for
encrypted vMotion. Omitted settings retain provider defaults; they do not enable
or disable encryption through Machine API conversion.

`storagePolicyName`, `hardwareVersion`, and `pciDevices` previously produced only
admission warnings despite failing conversion. They now deny admission so an
unsupported request fails before synchronization. Existing objects with these
values also fail conversion; they must use supported settings before migration.
The warning-only behavior for `network.devices[*].macAddr` is unchanged.

## IPAM references

Only `ipam.cluster.x-k8s.io` pools of Kind `InClusterIPPool` or
`GlobalInClusterIPPool` are supported. Their Machine API resource names are
`inclusterippools` and `globalinclusterippools`, respectively. Both conversion
directions reject unknown or empty groups and unknown kinds/resources instead
of guessing a mapping. Existing custom-pool users must use a supported mapping
before migration. Admission applies the same restriction to CAPV pool references.

Other deny validations remain defined in
[`unsupported-vsphere-spec-fields.yaml`](../admission-policies/vsphere/unsupported-vsphere-spec-fields.yaml).

## Observed power-state preservation

Machine API to Cluster API conversion carries `poweredOn`, `poweredOff`, and
`suspended` observations in `machine.openshift.io/instance-state` on the core
and infrastructure Machines. Nonempty provider status takes precedence over a
conflicting source annotation; otherwise the source annotation is used. Reverse
conversion restores recognized power states from the core Machine annotation
into Machine API provider status and metadata.

This annotation is a last-observed snapshot, not live power telemetry. CAPV does
not refresh it, so it can become stale while Cluster API is authoritative. This
preservation does not establish safe authority handback.

Provisioning readiness remains separate and unchanged: without a recognized
power snapshot, reverse conversion still emits the legacy synthetic `ready`
state when CAPV `Ready` is true. Machine API to CAPV conversion still recognizes
only that synthetic state as ready. Consequently, preserving real power states
does not fix readiness or MachineCreation-condition round trips; that contract
requires separate work.
