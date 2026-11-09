/*
Copyright 2026 Red Hat, Inc.

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
package pods

import (
	"context"
	"errors"
	"flag"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestUpgradeableCommands(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgradeable Command Helpers")
}

var _ = Describe("Entrypoint arguments", func() {
	step := func(context.Context, client.Client, string, string) error {
		Fail("argument handling must finish before running the assessment")
		return nil
	}

	DescribeTable("should require exactly two version arguments",
		func(args []string) {
			err := Run(context.Background(), "upgradeable-test", args, step)
			Expect(err).To(MatchError(errInvalidArguments))
		},
		Entry("missing both versions", []string{}),
		Entry("missing target version", []string{"4.16.0"}),
		Entry("extra argument", []string{"4.16.0", "4.17.0", "extra"}),
	)
	DescribeTable("should support standard help flags without an API client",
		func(arg string) {
			err := Run(context.Background(), "upgradeable-test", []string{arg}, step)
			Expect(err).To(MatchError(flag.ErrHelp))
		},
		Entry("short help", "-h"),
		Entry("long help", "--help"),
	)
})

var _ = Describe("Loop error classification", func() {
	DescribeTable("should classify API errors",
		func(err error, terminal bool) { Expect(IsTerminal(APIError(err))).To(Equal(terminal)) },
		Entry("forbidden is terminal", apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "images", errors.New("denied")), true),
		Entry("invalid request is terminal", apierrors.NewBadRequest("bad input"), true),
		Entry("conflicts can recover", apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "images", errors.New("conflict")), false),
		Entry("timeouts can recover", apierrors.NewTimeoutError("timeout", 1), false),
		Entry("network errors can recover", errors.New("connection refused"), false),
	)
	It("should stop after a terminal step failure", func() {
		failure := errors.New("missing manifest")
		attempts := 0
		err := Loop(context.Background(), logr.Discard(), func() error { attempts++; return Terminal(failure) })
		Expect(err).To(MatchError(failure))
		Expect(attempts).To(Equal(1))
	})
	It("should honor cancellation before running the step", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := Loop(ctx, logr.Discard(), func() error { Fail("step must not run"); return nil })
		Expect(err).To(MatchError(context.Canceled))
	})
})
