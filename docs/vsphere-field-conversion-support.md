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

Other deny validations remain defined in
[`unsupported-vsphere-spec-fields.yaml`](../admission-policies/vsphere/unsupported-vsphere-spec-fields.yaml).
