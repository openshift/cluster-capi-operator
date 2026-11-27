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

## Clone mode and network defaults

CAPV Machines and MachineTemplates must explicitly set `cloneMode` to `fullClone`
or `linkedClone`. Omitted or empty CAPV clone mode is rejected by conversion and
admission: CAPV can select linked cloning based on available snapshots, whereas
Machine API omission means full cloning. The converter does not query vCenter.
Existing CAPV objects with omitted mode need an explicit, intentionally chosen
mode before conversion or policy-covered updates can succeed. Machine API
omission remains supported and converts to explicit CAPV `fullClone`.

Machine API conversion returns CAPV `powerOffMode: hard`. IPv4 DHCP is derived
from the absence of static addresses and IPAM pools. Focused round-trip tests
assert these defaults separately from explicit clone-mode preservation.

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

Provisioning readiness is separate from power. Machine API to CAPV conversion
uses the same phase rule as the core Cluster API Machine: `Provisioned`, `Running`
and `Deleting` indicate infrastructure provisioned; other present phases do not.
Thus a provisioned, powered-off VM remains provisioned, while a powered-on VM in
`Provisioning` does not become ready merely from power or early creation success.
`Deleting` follows the existing core conversion convention, not a health claim.
Only when the phase is absent does the legacy synthetic `ready` state supply
readiness. Reverse conversion still emits synthetic `ready` when CAPV `Ready` is
true and no recognized power snapshot exists; this is a derived default, not an
observed power state. None of these mappings proves safe authority handback.
