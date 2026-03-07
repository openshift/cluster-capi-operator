package main

import (
	"flag"
	"os"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	// Import test packages to register tests
	_ "github.com/openshift/cluster-capi-operator/e2e"
)

func main() {
	flag.Parse()
	gomega.RegisterFailHandler(ginkgo.Fail)

	success := ginkgo.RunSpecs(ginkgo.GinkgoT(), "Cluster CAPI Operator E2E Suite")
	if !success {
		os.Exit(1)
	}
}
