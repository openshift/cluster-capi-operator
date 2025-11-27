package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/spf13/cobra"

	// If using ginkgo, import your tests here.
	_ "github.com/openshift/cluster-capi-operator/e2e"
)

func main() {
	extensionRegistry := e.NewRegistry()
	capiExtension := e.NewExtension("openshift", "payload", "cluster-capi-operator")

	capiExtension.AddSuite(e.Suite{
		Name:       "capio/parallel",
		Parents: []string{
			"openshift/conformance/parallel",
		},
		Qualifiers: []string{`!labels.exists(l, l == "Serial") && !labels.exists(l, l == "Disruptive")`},
	})

	capiExtension.AddSuite(e.Suite{
		Name:       "capio/serial",
		Parents: []string{
			"openshift/conformance/serial",
		},
		Qualifiers: []string{`labels.exists(l, l == "Serial") && !labels.exists(l, l == "Disruptive")`},
	})

	capiExtension.AddSuite(e.Suite{
		Name: "capio/disruptive",
		Parents: []string{
			"openshift/disruptive-longrunning",
		},
		Qualifiers: []string{`labels.exists(l, l == "Disruptive")`},
	})

	capiExtension.AddSuite(e.Suite{
		Name:       "capio/e2e",
		Description: "All Cloud CAPI Operator tests",
	})

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	// Apply filters
	applyFilters(specs)

	capiExtension.AddSpecs(specs)
	extensionRegistry.Register(capiExtension)

	root := &cobra.Command{
		Long: "Cluster CAPI Operator tests extension for OpenShift",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(extensionRegistry)...)

	if err := func() error {
		return root.Execute()
	}(); err != nil {
		os.Exit(1)
	}
}

func applyFilters(specs et.ExtensionTestSpecs) {
	// Apply Platform label filters: tests with Platform:platformname only run on that platform
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if strings.HasPrefix(label, "Platform:") {
				platformName := strings.TrimPrefix(label, "Platform:")
				spec.Include(et.PlatformEquals(platformName))
			}
		}
	})

	// Apply NoPlatform label filters: tests with NoPlatform:platformname excluded from that platform
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if strings.HasPrefix(label, "NoPlatform:") {
				platformName := strings.TrimPrefix(label, "NoPlatform:")
				spec.Exclude(et.PlatformEquals(platformName))
			}
		}
	})

	// Exclude all cluster-capi-operator tests on single-node clusters
	// Single-node clusters don't use Machine API (no Machines/MachineSets exist),
	// so all CAPI tests that interact with Machine API are incompatible with SNO.
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		spec.Exclude(et.TopologyEquals("SingleReplica"))
	})
}
