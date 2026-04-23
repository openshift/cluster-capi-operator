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
package commoncmdoptions_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	utiltls "github.com/openshift/controller-runtime-common/pkg/tls"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

// testExtraFlag* exercise InitOperatorConfig's extraFlags merge; names must not
// collide with CAPI manager or leader-election flags.
const (
	testExtraFlagName    = "capi-operator-test-extra-string"
	testExtraFlagDefault = "default-extra-test-value"
	testExtraFlagCustom  = "custom-extra-test-value"
)

// configOutput is the JSON output from the re-exec'd subprocess.
type configOutput struct {
	CAPINamespace     string `json:"capiNamespace"`
	MAPINamespace     string `json:"mapiNamespace"`
	OperatorNamespace string `json:"operatorNamespace"`
	HealthAddr        string `json:"healthAddr"`
	LeaderElect       bool   `json:"leaderElect"`
	LeaderElectionID  string `json:"leaderElectionID"` //nolint:tagliatelle
	LeaderElectionNS  string `json:"leaderElectionNS"` //nolint:tagliatelle
	LeaseDurationSecs int    `json:"leaseDurationSecs"`
	RenewDeadlineSecs int    `json:"renewDeadlineSecs"`
	RetryPeriodSecs   int    `json:"retryPeriodSecs"`
	ManagerHealthAddr string `json:"managerHealthAddr,omitempty"`
	ManagerExitReason string `json:"managerExitReason,omitempty"`

	// TLS: resolved manager options (after CLI override logic).
	TLSMinVersion   uint16   `json:"tlsMinVersion"`
	TLSCipherSuites []string `json:"tlsCipherSuites"`

	// ExtraTestString is from the test-only extra flag.FlagSet passed to InitOperatorConfig.
	ExtraTestString string `json:"extraTestString"`
}

var (
	kubeconfigPath string
	testEnv        *envtest.Environment
	cl             client.Client
)

type execMode string

const (
	execModeInitConfig execMode = "initConfig"

	reexecModeEnvVar = "CAPIO_TEST_REEXEC"
)

func TestMain(m *testing.M) {
	// In reexec mode, the test reinvokes its own binary, passing the desired
	// execution mode as an environment variable.
	execMode := execMode(os.Getenv(reexecModeEnvVar))
	if execMode == execModeInitConfig {
		runInitConfig()
		return
	}

	// Normal test path: start envtest, create APIServer object, run tests.
	code, err := runTests(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	os.Exit(code)
}

func runTests(m *testing.M) (_ int, err error) {
	testEnv = &envtest.Environment{}

	_, cl, err = test.StartEnvTest(testEnv)
	if err != nil {
		return 1, fmt.Errorf("failed to start envtest: %w", err)
	}

	defer func() { err = errors.Join(err, testEnv.Stop()) }()

	// Create the APIServer object required by resolveTLSProfile.
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: utiltls.APIServerName,
		},
	}
	if err := cl.Create(context.Background(), apiServer); err != nil {
		return 1, fmt.Errorf("failed to create APIServer object: %w", err)
	}

	// Write kubeconfig to a temp file for subprocesses.
	kubeconfigPath, err = writeKubeconfig(testEnv.KubeConfig)
	if err != nil {
		return 1, fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	defer func() { err = errors.Join(err, os.Remove(kubeconfigPath)) }()

	return m.Run(), nil
}

// reexecCmd returns a command that re-executes the test binary with the given
// mode, passing flags as command arguments.
func reexecCmd(t *testing.T, mode execMode, flags []string) *exec.Cmd {
	t.Helper()

	// The test binary path. When run via `go test`, os.Args[0] is the
	// compiled test binary.
	cmd := exec.CommandContext(t.Context(), os.Args[0])
	cmd.Args = append([]string{os.Args[0]}, flags...)
	cmd.Env = append(
		// Start with a minimal environment to avoid polluting the subprocess.
		filterEnv(os.Environ(), "HOME", "PATH", "KUBEBUILDER_ASSETS", "TMPDIR"),
		reexecModeEnvVar+"="+string(mode),
		"KUBECONFIG="+kubeconfigPath,
	)

	return cmd
}

// --- Re-exec binary modes ---

// runInitConfig is the subprocess entry point for testing InitOperatorConfig.
// It calls InitOperatorConfig with the flags from os.Args and prints the
// resulting configuration as JSON to stdout.
func runInitConfig() {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))

	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build rest config: %v\n", err)
		os.Exit(1)
	}

	extraFlags := flag.NewFlagSet("", flag.ContinueOnError)
	extraTestString := extraFlags.String(testExtraFlagName, testExtraFlagDefault, "test-only flag merged via extraFlags")

	_, operatorConfig, mgrOpts, _, err := commoncmdoptions.InitOperatorConfig(ctx, cfg, scheme, "test-manager", "test-namespace", extraFlags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "InitOperatorConfig error: %v\n", err)
		os.Exit(1)
	}

	result := configOutput{
		CAPINamespace:     ptr.Deref(operatorConfig.CAPINamespace, ""),
		MAPINamespace:     ptr.Deref(operatorConfig.MAPINamespace, ""),
		OperatorNamespace: ptr.Deref(operatorConfig.OperatorNamespace, ""),
		HealthAddr:        mgrOpts.HealthProbeBindAddress,
		LeaderElect:       mgrOpts.LeaderElection,
		LeaderElectionID:  mgrOpts.LeaderElectionID,
		LeaderElectionNS:  mgrOpts.LeaderElectionNamespace,
		ExtraTestString:   ptr.Deref(extraTestString, ""),
	}

	// Apply TLSOptions to a tls.Config to inspect what was configured.
	tlsCfg := &tls.Config{}
	for _, opt := range operatorConfig.TLSOptions {
		opt(tlsCfg)
	}

	result.TLSMinVersion = tlsCfg.MinVersion
	for _, id := range tlsCfg.CipherSuites {
		result.TLSCipherSuites = append(result.TLSCipherSuites, tls.CipherSuiteName(id))
	}

	if mgrOpts.LeaseDuration != nil {
		result.LeaseDurationSecs = int(mgrOpts.LeaseDuration.Seconds())
	}

	if mgrOpts.RenewDeadline != nil {
		result.RenewDeadlineSecs = int(mgrOpts.RenewDeadline.Seconds())
	}

	if mgrOpts.RetryPeriod != nil {
		result.RetryPeriodSecs = int(mgrOpts.RetryPeriod.Seconds())
	}

	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode output: %v\n", err)
		os.Exit(1)
	}
}

// --- Tests ---

func TestInitOperatorConfig(t *testing.T) {
	tests := []struct {
		name      string
		flags     []string
		expectErr bool
		verify    func(t *testing.T, result configOutput)
	}{
		{
			name:  "default flags",
			flags: nil,
			verify: func(t *testing.T, result configOutput) {
				assertEqual(t, "CAPINamespace", result.CAPINamespace, controllers.DefaultCAPINamespace)
				assertEqual(t, "MAPINamespace", result.MAPINamespace, controllers.DefaultMAPINamespace)
				assertEqual(t, "OperatorNamespace", result.OperatorNamespace, controllers.DefaultOperatorNamespace)
				assertEqual(t, "HealthAddr", result.HealthAddr, ":9440")
				assertEqual(t, "LeaderElect", result.LeaderElect, true)
				assertEqual(t, "LeaderElectionID", result.LeaderElectionID, "test-manager-leader")
				assertEqual(t, "LeaderElectionNS", result.LeaderElectionNS, "test-namespace")
				assertEqual(t, "LeaseDurationSecs", result.LeaseDurationSecs, int(commoncmdoptions.LeaseDuration.Seconds()))
				assertEqual(t, "RenewDeadlineSecs", result.RenewDeadlineSecs, int(commoncmdoptions.RenewDeadline.Seconds()))
				assertEqual(t, "RetryPeriodSecs", result.RetryPeriodSecs, int(commoncmdoptions.RetryPeriod.Seconds()))
				assertEqual(t, "ExtraTestString", result.ExtraTestString, testExtraFlagDefault)
			},
		},
		{
			name: "custom namespaces",
			flags: []string{
				"--capi-namespace=custom-capi",
				"--mapi-namespace=custom-mapi",
				"--operator-namespace=custom-operator",
				"--" + testExtraFlagName + "=" + testExtraFlagCustom,
			},
			verify: func(t *testing.T, result configOutput) {
				assertEqual(t, "CAPINamespace", result.CAPINamespace, "custom-capi")
				assertEqual(t, "MAPINamespace", result.MAPINamespace, "custom-mapi")
				assertEqual(t, "OperatorNamespace", result.OperatorNamespace, "custom-operator")
				assertEqual(t, "ExtraTestString", result.ExtraTestString, testExtraFlagCustom)
			},
		},
		{
			name:  "custom health address",
			flags: []string{"--health-addr=:0"},
			verify: func(t *testing.T, result configOutput) {
				assertEqual(t, "HealthAddr", result.HealthAddr, ":0")
			},
		},
		{
			name:  "leader election disabled",
			flags: []string{"--leader-elect=false"},
			verify: func(t *testing.T, result configOutput) {
				assertEqual(t, "LeaderElect", result.LeaderElect, false)
			},
		},
		{
			name:  "TLS uses cluster profile when no CLI flags set",
			flags: nil,
			verify: func(t *testing.T, result configOutput) {
				// Should use the cluster profile's min version.
				assertEqual(t, "TLSMinVersion", result.TLSMinVersion, tls.VersionTLS12)

				// Cipher suites should be populated from the cluster profile.
				if len(result.TLSCipherSuites) == 0 {
					t.Error("TLSCipherSuites should not be empty when using cluster profile")
				}
			},
		},
		{
			name:  "TLS flags overridden by CLI",
			flags: []string{"--tls-min-version=VersionTLS11", "--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
			verify: func(t *testing.T, result configOutput) {
				// Both manager values should come from the CLI.
				assertEqual(t, "TLSMinVersion", result.TLSMinVersion, uint16(tls.VersionTLS11))

				wantCiphers := []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}
				if !slices.Equal(result.TLSCipherSuites, wantCiphers) {
					t.Errorf("TLSCipherSuites: got %v, want %v", result.TLSCipherSuites, wantCiphers)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("executing initConfig with flags: %v", tc.flags)
			cmd := reexecCmd(t, execModeInitConfig, tc.flags)

			stdout, err := cmd.Output()

			var stderr string

			exitErr := &exec.ExitError{}
			if errors.As(err, &exitErr) {
				stderr = string(exitErr.Stderr)
			}

			if tc.expectErr {
				if err == nil {
					t.Fatal("expected error but got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
			}

			var result configOutput
			if jsonErr := json.Unmarshal(stdout, &result); jsonErr != nil {
				t.Fatalf("failed to parse JSON output: %v\nstdout: %s", jsonErr, string(stdout))
			}

			tc.verify(t, result)
		})
	}
}
