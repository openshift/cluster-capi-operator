/*
Copyright 2025 Red Hat, Inc.

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

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/spf13/cobra"

	e2e "github.com/openshift/cluster-capi-operator/e2e"
)

func main() {
	extensionRegistry := e.NewRegistry()

	ext := e.NewExtension("openshift", "payload", "cluster-capi-operator")

	defaultTimeout := 30 * time.Minute
	disruptiveTimeout := 90 * time.Minute

	ext.AddSuite(e.Suite{
		Name:        "capio/parallel",
		Qualifiers:  []string{`!labels.exists(l, l == "Serial") && !labels.exists(l, l == "Disruptive")`},
		Parents:     []string{"openshift/conformance/parallel"},
		TestTimeout: &defaultTimeout,
	})

	ext.AddSuite(e.Suite{
		Name:        "capio/serial",
		Qualifiers:  []string{`labels.exists(l, l == "Serial") && !labels.exists(l, l == "Disruptive")`},
		Parents:     []string{"openshift/conformance/serial"},
		Parallelism: 1,
		TestTimeout: &defaultTimeout,
	})

	ext.AddSuite(e.Suite{
		Name:             "capio/disruptive",
		Qualifiers:       []string{`labels.exists(l, l == "Disruptive")`},
		Parents:          []string{"openshift/disruptive-longrunning"},
		Parallelism:      1,
		ClusterStability: e.ClusterStabilityDisruptive,
		TestTimeout:      &disruptiveTimeout,
	})

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %v", err))
	}

	specs.AddBeforeAll(func() {
		e2e.InitCommonVariables()
	})

	// Auto-apply platform environment selectors from Ginkgo Label("platform:<name>") annotations.
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if platform, ok := strings.CutPrefix(label, "platform:"); ok {
				spec.Include(et.PlatformEquals(platform))
			}
		}
	})

	ext.AddSpecs(specs)

	extensionRegistry.Register(ext)

	root := &cobra.Command{
		Long: "Cluster CAPI Operator tests extension for OpenShift",
	}
	root.AddCommand(cmd.DefaultExtensionCommands(extensionRegistry)...)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
