# Ubiquitous Language

## APIs & Namespaces

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Machine API** | OpenShift's native machine management API for provisioning and managing compute instances | MAPI (in user-facing text) |
| **Cluster API** | Upstream Kubernetes SIG project providing declarative machine lifecycle management | CAPI (in user-facing text) |
| **MAPI Namespace** | The `openshift-machine-api` namespace where Machine API resources live | |
| **CAPI Namespace** | The `openshift-cluster-api` namespace where Cluster API resources live | |
| **Operator Namespace** | The `openshift-cluster-api-operator` namespace where the operator itself is deployed | |

## Machine Resources

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Instance** | The actual cloud compute resource (e.g. an EC2 instance, an Azure VM) that a MAPI Machine or Infrastructure Machine represents | VM, server, host |
| **MAPI Machine** | A Machine API resource representing an Instance; contains both generic and platform-specific configuration in a single object | unqualified "Machine" |
| **CAPI Machine** | A Cluster API resource representing the generic aspects of an Instance; references a separate Infrastructure Machine for platform-specific configuration | unqualified "Machine" |
| **MAPI MachineSet** | A Machine API set of identically-configured MAPI Machines with a desired replica count | unqualified "MachineSet" |
| **CAPI MachineSet** | A Cluster API set of identically-configured CAPI Machines with a desired replica count; references an Infrastructure Template | unqualified "MachineSet" |
| **Infrastructure Machine** | A CAPI platform-specific resource (e.g. AWSMachine, AzureMachine) representing the Instance backing a CAPI Machine. Commonly abbreviated to **InfraMachine** | provider machine |
| **Infrastructure Template** | A platform-specific template (e.g. AWSMachineTemplate) used by a CAPI MachineSet to stamp out new Infrastructure Machines. Commonly abbreviated to **InfraTemplate** | MachineTemplate (ambiguous) |

## Cluster Resources

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Core Cluster** | The Cluster API Cluster resource representing the OpenShift cluster itself | CAPI Cluster |
| **Infrastructure Cluster** | A platform-specific cluster resource (e.g. AWSCluster) representing the cloud infrastructure state of the cluster. Commonly abbreviated to **InfraCluster** | |
| **Infrastructure** | The OpenShift `config.openshift.io/v1` singleton resource that identifies the cluster's platform type | Infra (ambiguous with Infrastructure Cluster) |

## Providers

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Core Provider** | A CAPI Operator resource representing the core Cluster API controller components | |
| **Infrastructure Provider** | A platform-specific project (e.g. CAPA for AWS, CAPZ for Azure) consisting of cloud-specific CRDs and controllers that operate on them; installed by the CAPI Operator using manifests from the Infrastructure Provider's container image | Cloud provider (ambiguous), unqualified "provider" |
| **Platform** | The specific cloud or infrastructure backend (AWS, Azure, GCP, vSphere, OpenStack, PowerVS, Bare Metal) | Provider (ambiguous with Infrastructure Provider) |

## Authority & Migration

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Authoritative API** | The API (Machine API or Cluster API) that currently owns and is the source of truth for a given resource | Owner, primary, active API |
| **Non-Authoritative** | The mirrored copy of a resource managed by the API that is not currently authoritative | Secondary, replica, shadow |
| **Migration** | The process of transferring authoritative control of a resource from one API to the other | Handover (acceptable synonym), switchover, cutover |
| **Migrating** | The transitional AuthoritativeAPI state during which control is being transferred between APIs | |

## Synchronization

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Synchronization** | The mechanism that keeps the authoritative and non-authoritative copies of a resource consistent | Sync (acceptable shorthand in code), replication, mirroring |
| **Synchronized Condition** | A status condition on a MAPI resource indicating whether **Synchronization** of its non-authoritative copy is current | Sync status |
| **Synchronized Generation** | The metadata generation at which a resource was last successfully synchronized | Sync generation |
| **Sync Finalizer** | A finalizer (`sync.machine.openshift.io/finalizer`) preventing premature deletion of non-authoritative resources | |

## Pausing

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Paused** | A state where a CAPI resource's reconciler stops processing it, controlled by the `cluster.x-k8s.io/paused` annotation | Frozen, suspended, disabled |
| **Paused Annotation** | The `cluster.x-k8s.io/paused` annotation whose presence requests a CAPI controller to stop reconciling a resource | |
| **Paused Condition** | A status condition set by CAPI reconcilers confirming the resource is paused | |

## Conversion

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Conversion** | The bidirectional transformation of resource specs and statuses between Machine API and Cluster API representations | Translation, mapping, transformation |
| **MAPI-to-CAPI Conversion** | Conversion direction from MAPI Machines or MAPI MachineSets to their CAPI equivalents plus Infrastructure Machines or Infrastructure Templates | |
| **CAPI-to-MAPI Conversion** | Conversion direction from CAPI Machines or CAPI MachineSets plus their Infrastructure Machines or Infrastructure Templates back to MAPI equivalents | |

## Machine Lifecycle

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **Phase** | A Machine API status field indicating the machine's lifecycle stage (Provisioning, Running, Deleting) | State (ambiguous) |
| **Node Ref** | A reference from a MAPI Machine or CAPI Machine to the Kubernetes Node object it backs | |
| **Readiness Gate** | A custom condition that must be true before a CAPI Machine is considered ready | |
| **Externally Managed** | An Infrastructure Cluster marked with `cluster.x-k8s.io/managed-by` indicating no **Infrastructure Provider** reconciles it | |

## Operator Status

| Term | Definition | Aliases to avoid |
| --- | --- | --- |
| **ClusterOperator** | An OpenShift resource tracking the operator's overall health via Available, Progressing, and Degraded conditions | Operator status |

## Relationships

- A **MAPI Machine** and a **CAPI Machine** exist as parallel resources linked by **Synchronization**, both representing the same **Instance**
- Exactly one of the two copies is the **Authoritative API** at any time; the other is **Non-Authoritative**
- A **MAPI MachineSet** owns zero or more **MAPI Machines**
- A **CAPI MachineSet** owns zero or more **CAPI Machines** and references an **Infrastructure Template**
- Each **CAPI Machine** references exactly one **Infrastructure Machine** via `InfrastructureRef`; a **MAPI Machine** contains its platform-specific configuration inline
- The **Core Cluster** references exactly one **Infrastructure Cluster** via `InfrastructureRef`
- An **Infrastructure Provider** manages the lifecycle of **Infrastructure Machines** and **Infrastructure Clusters** for a given **Platform**
- **Migration** transitions the **Authoritative API** from one API to the other, passing through the **Migrating** state
- During **Migration**, the old authority is **Paused** to prevent conflicting reconciliation
- **Conversion** is used by **Synchronization** to translate resources between the two API representations

## Example dialogue

> **Dev:** "When a user wants to move a MAPI Machine to Cluster API, what happens?"
>
> **Domain expert:** "They set `Spec.AuthoritativeAPI` to Cluster API on the **MAPI Machine**. The migration controller detects the mismatch with `Status.AuthoritativeAPI` and begins a **Migration**."
>
> **Dev:** "So both controllers are running during Migration?"
>
> **Domain expert:** "Both controllers are always running. The migration controller sets the status to **Migrating**, then requests the old **Authoritative API**'s resource to be **Paused**. It waits for the **Paused Condition** to confirm the old controller has stopped reconciling that specific resource, so only one controller is operating on any given **Instance** at a time."
>
> **Dev:** "And the Non-Authoritative copy -- is it just a mirror?"
>
> **Domain expert:** "Exactly. The sync controller performs **Conversion** from the **Authoritative** copy and updates the **Non-Authoritative** one. It tracks this via the **Synchronized Condition** and **Synchronized Generation** so we know when they've diverged."
>
> **Dev:** "What about the Infrastructure Machine? Does it get duplicated too?"
>
> **Domain expert:** "A **MAPI Machine** doesn't have a separate **Infrastructure Machine** -- it contains the platform-specific configuration inline in a single object. A **CAPI Machine** splits that out into a separate **Infrastructure Machine** resource. During **Conversion**, the sync controller extracts the platform-specific parts of the **MAPI Machine** into the **Infrastructure Machine**, or merges them back the other way. The **Sync Finalizer** prevents the **Infrastructure Machine** from being deleted before both sides are cleaned up."

## Flagged ambiguities

- **"Provider"** is used to mean both the cloud platform (AWS, Azure) and the platform-specific project (**Infrastructure Provider**). Use **Platform** for the cloud backend and **Infrastructure Provider** for the project and its CRDs and controllers.
- **"Infrastructure"/"Infra"** is overloaded across three concepts: the OpenShift **Infrastructure** singleton (platform detection), **Infrastructure Cluster** (cloud cluster state), and **Infrastructure Machine** (cloud instance). Always use the full compound term.
- **"Template"** can refer to either a CAPI MachineSet's machine template spec or the platform-specific **Infrastructure Template** resource. Use **Infrastructure Template** for the platform-specific resource.
- **"Sync"** is acceptable in code identifiers but user-facing text should use **Synchronization** to avoid ambiguity with other sync concepts (e.g. secret sync, which is a separate controller syncing secrets between namespaces, not machine state).
- **"Machine"/"MachineSet"** without a qualifier is ambiguous — both APIs define these resources with different structures. Always qualify as **MAPI Machine**/**CAPI Machine** and **MAPI MachineSet**/**CAPI MachineSet**.
- **"MAPI"/"CAPI"** are acceptable in code and internal identifiers but user-facing text (logs, errors, docs) should use **Machine API** and **Cluster API** per project convention.
