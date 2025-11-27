package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/spf13/cobra"

	// If using ginkgo, import your tests here.
	_ "github.com/openshift/cluster-capi-operator/e2e"
)

func main() {
	extensionRegistry := e.NewRegistry()
	capiExtension := e.NewExtension("openshift", "payload", "cluster-capi-operator")

	capiExtension.AddSuite(e.Suite{
		Name:       "capio/conformance/parallel",
		Qualifiers: []string{`!labels.exists(l, l == "Serial") && labels.exists(l, l == "Conformance")`},
	})

	capiExtension.AddSuite(e.Suite{
		Name:       "capio/conformance/serial",
		Qualifiers: []string{`labels.exists(l, l == "Serial") && labels.exists(l, l == "Conformance")`},
	})

	capiExtension.AddSuite(e.Suite{
		Name:       "capio/e2e",
		Qualifiers: []string{`name.contains("[Feature:ClusterAPI]") || name.contains("[OCPFeatureGate:MachineAPIMigration]")`},
	})

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	// Let's scan for tests with a platform label and create the rule for them such as [platform:vsphere]
	foundPlatforms := make(map[string]string)

	for _, test := range specs.Select(extensiontests.NameContains("[platform:")).Names() {
		re := regexp.MustCompile(`\[platform:[a-z]*]`)

		match := re.FindStringSubmatch(test)
		for _, platformDef := range match {
			if _, ok := foundPlatforms[platformDef]; !ok {
				platform := platformDef[strings.Index(platformDef, ":")+1 : len(platformDef)-1]
				foundPlatforms[platformDef] = platform
				specs.Select(extensiontests.NameContains(platformDef)).
					Include(extensiontests.PlatformEquals(platform))
			}
		}
	}

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
