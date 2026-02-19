---
name: test-standards
description: Apply when writing, modifying, or reviewing test files (_test.go). Covers test level selection, assertion quality, BDD naming, and use of shared helpers.
---

When writing tests, follow these standards. When reviewing tests, flag violations and suggest fixes with code examples.

## Choosing the right test level

Pick the cheapest level that can adequately cover the behaviour. Do not escalate without reason.

**Unit test** (no client or fake client, no envtest) — for pure logic, conversions, single-function behaviour, error paths that don't depend on server-side behaviour. Use `fake.NewClientBuilder()` only when the test doesn't depend on realistic API server responses (field defaulting, status subresource semantics, conflict errors, SSA merge, etc.). These run in milliseconds.

**Integration test** (envtest) — for anything that interacts with a Kubernetes API: controller reconciliation loops, multi-resource interactions, watching, status updates, and any scenario where fake client behaviour diverges from a real API server. Prefer envtest over fakes when in doubt — faking accurately is hard and flaky fakes waste more time than the slower test. Use `pkg/test.StartEnvTest()` in `suite_test.go`. These run in seconds.

**E2E test** (`e2e/`) — only for behaviour that requires real infrastructure: actual machine provisioning, cloud API interactions, cross-component migration flows. These run in minutes.

Rules of thumb:
- If you're testing "does this function return the right value/error" and it doesn't need a client → unit test.
- If you're testing any controller or client interaction → integration test (envtest).
- If you're testing "does a real machine appear in the cloud" → e2e test.
- If envtest can reproduce the scenario, do not write an e2e test.

## Use existing shared helpers

Before writing new test utilities, builders, matchers, or setup code, search the repo for existing ones — particularly in `pkg/test/`, `pkg/conversion/test/`, `pkg/admissionpolicy/testutils/`, `e2e/framework/`, and the vendored `testutils/resourcebuilder/` package. Do not duplicate what already exists. If you need a variant, extend the existing helper rather than creating a parallel one.

## Debuggable failures

Every test failure must be debuggable from the output alone — without reading test source code. This means two things:

**1. Assertion messages.** If a failure's stack trace and default matcher output wouldn't tell you what went wrong, add a description. This applies especially to generic matchers like `BeNil()`, `BeTrue()`, `HaveLen()` where the default output doesn't convey intent.

```go
// good — failure output explains the scenario
Expect(transport).To(BeNil(), "expected nil transport when additionalTrustedCA is not set")

// bad — failure output is just "expected nil, got &http.Transport{...}"
Expect(transport).To(BeNil())
```

**2. Stack traces.** Do not call `Expect`, `Fail`, or panic from helper functions — failures will point at the helper, not the test that called it. Return errors to the calling test instead.

If assertions inside a helper are unavoidable, use `GinkgoHelper()` so the stack trace shows the caller:

```go
func expectResourceReady(obj client.Object) {
    GinkgoHelper()
    Expect(obj.GetAnnotations()).To(HaveKey("ready"))
}
```

## BDD test names

Describe **expected behaviour**, not implementation details. Names should read as specifications.

```go
// good — describes behaviour
It("should reject machines with duplicate provider IDs", func() { ... })

// bad — describes implementation
It("should return an error from validateProviderID", func() { ... })
```

Use nested `Context` blocks with `when` to set up preconditions:

```go
Context("when the infrastructure cluster is not ready", func() {
    It("should requeue after 30 seconds", func() { ... })
})
```

Use `By()` to document distinct phases within a test.

## Concise assertions

Prefer precise matchers over multiple loose ones. Combine into one `Expect` when possible.

```go
// good — single assertion, exact match
Expect(err).To(MatchError(expectedErr))

// bad — two assertions, string matching
Expect(err).To(HaveOccurred())
Expect(err).To(MatchError(ContainSubstring("connection refused")))
```

When an expected error is reused across multiple test cases, declare it as a variable rather than duplicating the literal:

```go
// good — declared once, reused across cases
expectedErr := fmt.Errorf("connection refused")
Expect(err).To(MatchError(expectedErr))

// bad — same string repeated in every test case
Expect(err).To(MatchError("connection refused"))
```

Combine related assertions into a single matcher (e.g., `SatisfyAll`, `ConsistOf`). With `Eventually`, each separate assertion polls with its own timeout — multiple assertions checking the same object multiply the wait time on failure.

## No sleeps, no timeout bumps

In event-driven systems, tests should wait for conditions, not for time to pass.

- **Never use `time.Sleep()`**. Use `Eventually` with a condition that checks the actual state you're waiting for.
- **Do not bump `Eventually` timeouts to fix flaky tests.** A flaky test means the test is waiting for the wrong condition or the code has a race. Fix the root cause.
- **`Consistently` durations should be meaningful.** Too-short durations prove nothing — the condition might change immediately after. Use a duration long enough to cover at least a few reconciliation cycles.

```go
// good — waits for the actual state change
Eventually(komega.Object(machine)).Should(HaveField("Status.Phase", Equal("Running")))

// bad — arbitrary sleep hoping the controller has finished
time.Sleep(5 * time.Second)
Expect(machine.Status.Phase).To(Equal("Running"))
```

## General

- Follow patterns from CLAUDE.md (Komega, DescribeTable, resource builders, testutils cleanup).
- Remove `FIt`/`FContext` before committing.
