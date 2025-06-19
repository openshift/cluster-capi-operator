package framework

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/scale"
	"k8s.io/klog"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type machineSetParams struct {
	msName            string
	clusterName       string
	failureDomain     string
	replicas          int32
	infrastructureRef corev1.ObjectReference
	userDataSecret    string
}

const machineSetOpenshiftLabelKey = "machine.openshift.io/cluster-api-machineset"

// NewMachineSetParams returns a new machineSetParams object.
func NewMachineSetParams(msName, clusterName, failureDomain string, replicas int32, infrastructureRef corev1.ObjectReference, userDataSecretName string) machineSetParams {
	Expect(msName).ToNot(BeEmpty())
	Expect(clusterName).ToNot(BeEmpty())
	Expect(infrastructureRef.APIVersion).ToNot(BeEmpty())
	Expect(infrastructureRef.Kind).ToNot(BeEmpty())
	Expect(infrastructureRef.Name).ToNot(BeEmpty())

	return machineSetParams{
		msName:            msName,
		clusterName:       clusterName,
		replicas:          replicas,
		infrastructureRef: infrastructureRef,
		failureDomain:     failureDomain,
		userDataSecret:    userDataSecretName,
	}
}

// CreateMachineSet creates a new MachineSet resource.
func CreateMachineSet(cl client.Client, params machineSetParams) *clusterv1.MachineSet {
	By(fmt.Sprintf("Creating MachineSet %q", params.msName))

	ms := &clusterv1.MachineSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MachineSet",
			APIVersion: "machine.openshift.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.msName,
			Namespace: CAPINamespace,
		},
		Spec: clusterv1.MachineSetSpec{
			Replicas:    &params.replicas,
			ClusterName: params.clusterName,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"machine.openshift.io/cluster-api-cluster": params.clusterName,
					machineSetOpenshiftLabelKey:                params.msName,
				},
			},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels: map[string]string{
						"machine.openshift.io/cluster-api-cluster": params.clusterName,
						machineSetOpenshiftLabelKey:                params.msName,
					},
				},
				Spec: clusterv1.MachineSpec{
					Bootstrap: clusterv1.Bootstrap{
						DataSecretName: &params.userDataSecret,
					},
					ClusterName:       params.clusterName,
					InfrastructureRef: params.infrastructureRef,
				},
			},
		},
	}

	if params.failureDomain != "" {
		ms.Spec.Template.Spec.FailureDomain = &params.failureDomain
	}

	Expect(cl.Create(ctx, ms)).To(Succeed())
	return ms
}

// WaitForMachineSetsDeleted polls until the given MachineSets are not found, and
// there are zero Machines found matching the MachineSet's label selector.
func WaitForMachineSetsDeleted(cl client.Client, machineSets ...*clusterv1.MachineSet) {
	for _, ms := range machineSets {
		By(fmt.Sprintf("Waiting for MachineSet %q to be deleted", ms.GetName()))
		Eventually(func() bool {
			selector := ms.Spec.Selector

			machines, err := GetMachines(cl, &selector)
			if err != nil || len(machines) != 0 {
				return false // Still have Machines, or other error.
			}

			err = cl.Get(ctx, client.ObjectKey{
				Name:      ms.GetName(),
				Namespace: ms.GetNamespace(),
			}, &clusterv1.MachineSet{})

			return apierrors.IsNotFound(err) // MachineSet and Machines were deleted.
		}, WaitLong, RetryMedium).Should(BeTrue())
	}
}

// DeleteMachineSets deletes the specified machinesets and returns an error on failure.
func DeleteMachineSets(cl client.Client, machineSets ...*clusterv1.MachineSet) {
	for _, ms := range machineSets {
		By(fmt.Sprintf("Deleting MachineSet %q", ms.GetName()))
		Expect(cl.Delete(ctx, ms)).To(Succeed())
	}
}

// WaitForMachineSet waits for the all Machines belonging to the named
// MachineSet to enter the "Running" phase, and for all nodes belonging to those
// Machines to be ready.
func WaitForMachineSet(cl client.Client, name string) {
	By(fmt.Sprintf("Waiting for MachineSet machines %q to enter Running phase", name))

	machineSet, err := GetMachineSet(cl, name)
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() error {
		machines, err := GetMachinesFromMachineSet(cl, machineSet)
		if err != nil {
			return err
		}

		replicas := pointer.Int32PtrDerefOr(machineSet.Spec.Replicas, 0)

		if len(machines) != int(replicas) {
			return fmt.Errorf("%q: found %d Machines, but MachineSet has %d replicas",
				name, len(machines), int(replicas))
		}

		running := FilterRunningMachines(machines)

		// This could probably be smarter, but seems fine for now.
		if len(running) != len(machines) {
			return fmt.Errorf("%q: not all Machines are running: %d of %d",
				name, len(running), len(machines))
		}

		for _, m := range running {
			node, err := GetNodeForMachine(cl, m)
			if err != nil {
				return err
			}

			if !isNodeReady(node) {
				return fmt.Errorf("%s: node is not ready", node.Name)
			}
		}

		return nil
	}, WaitOverLong, RetryMedium).Should(Succeed())
}

// GetMachineSet gets a machineset by its name from the default machine API namespace.
func GetMachineSet(cl client.Client, name string) (*clusterv1.MachineSet, error) {
	machineSet := &clusterv1.MachineSet{}
	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}

	if err := cl.Get(ctx, key, machineSet); err != nil {
		return nil, fmt.Errorf("error querying api for machineSet object: %w", err)
	}

	return machineSet, nil
}

// GetMachinesFromMachineSet returns an array of machines owned by a given machineSet
func GetMachinesFromMachineSet(cl client.Client, machineSet *clusterv1.MachineSet) ([]*clusterv1.Machine, error) {
	machines, err := GetMachines(cl)
	if err != nil {
		return nil, fmt.Errorf("error getting machines: %w", err)
	}
	var machinesForSet []*clusterv1.Machine
	for key := range machines {
		if metav1.IsControlledBy(machines[key], machineSet) {
			machinesForSet = append(machinesForSet, machines[key])
		}
	}
	return machinesForSet, nil
}

// GetLatestMachineFromMachineSet returns the new created machine by a given machineSet.
func GetLatestMachineFromMachineSet(cl client.Client, machineSet *clusterv1.MachineSet) (*clusterv1.Machine, error) {
	machines, err := GetMachinesFromMachineSet(cl, machineSet)
	if err != nil {
		return nil, fmt.Errorf("error getting machines: %w", err)
	}

	var machine *clusterv1.Machine

	newest := time.Date(2020, 0, 1, 12, 0, 0, 0, time.UTC)

	for key := range machines {
		time := machines[key].CreationTimestamp.Time
		if time.After(newest) {
			newest = time
			machine = machines[key]
		}
	}

	return machine, nil
}

// DeleteMachines deletes the specified machines and returns an error on failure.
func DeleteMachines(cl client.Client, machines ...*clusterv1.Machine) error {
	return wait.PollUntilContextTimeout(ctx, RetryShort, time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, machine := range machines {
			if err := cl.Delete(ctx, machine); err != nil {
				klog.Errorf("Error querying api for machine object %q: %v, retrying...", machine.Name, err)
				return false, err
			}
		}

		return true, nil
	})
}

// WaitForMachinesDeleted polls until the given Machines are not found.
func WaitForMachinesDeleted(cl client.Client, machines ...*clusterv1.Machine) {
	Eventually(func() bool {
		for _, m := range machines {
			if err := cl.Get(context.Background(), client.ObjectKey{
				Name:      m.GetName(),
				Namespace: m.GetNamespace(),
			}, &clusterv1.Machine{}); !apierrors.IsNotFound(err) {
				return false // Not deleted, or other error.
			}
		}

		return true // Everything was deleted.
	}, WaitLong, RetryMedium).Should(BeTrue(), "error encountered while waiting for Machines to be deleted.")
}

// ScaleMachineSet scales a machineSet with a given name to the given number of replicas.
func ScaleMachineSet(name string, replicas int) error {
	scaleClient, err := getScaleClient()
	if err != nil {
		return fmt.Errorf("error calling getScaleClient %w", err)
	}

	scale, err := scaleClient.Scales(CAPINamespace).Get(ctx, schema.GroupResource{Group: "cluster.x-k8s.io", Resource: "MachineSet"}, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error calling scaleClient.Scales get: %w", err)
	}

	scaleUpdate := scale.DeepCopy()
	scaleUpdate.Spec.Replicas = int32(replicas)

	_, err = scaleClient.Scales(CAPINamespace).Update(ctx, schema.GroupResource{Group: "cluster.x-k8s.io", Resource: "MachineSet"}, scaleUpdate, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error calling scaleClient.Scales update: %w", err)
	}

	return nil
}

// getScaleClient returns a ScalesGetter object to manipulate scale subresources.
func getScaleClient() (scale.ScalesGetter, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("error getting config %w", err)
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("error calling rest.HTTPClientFor %w", err)
	}

	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("error calling NewDiscoveryRESTMapper %w", err)
	}

	discovery := discovery.NewDiscoveryClientForConfigOrDie(cfg)
	scaleKindResolver := scale.NewDiscoveryScaleKindResolver(discovery)

	scaleClient, err := scale.NewForConfig(cfg, mapper, dynamic.LegacyAPIPathResolverFunc, scaleKindResolver)
	if err != nil {
		return nil, fmt.Errorf("error calling building scale client %w", err)
	}

	return scaleClient, nil
}
