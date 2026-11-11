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
package upgradeable

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Pod failure detail", func() {
	podStatus := func(status corev1.PodStatus) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "assessment"}, Status: status}
	}

	DescribeTable("should describe the failure",
		func(status corev1.PodStatus, expected string) {
			Expect(podFailureDetail(podStatus(status))).To(Equal(expected))
		},
		Entry("terminated container",
			corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "assessment",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: "boom"}},
			}}},
			"exit code 1 (Error): boom"),
		Entry("last termination state only",
			corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{
				Name:                 "assessment",
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 2, Reason: "OOMKilled"}},
			}}},
			"exit code 2 (OOMKilled)"),
		Entry("pod level reason and message",
			corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded", Message: "Pod was active on the node longer than the specified deadline"},
			"DeadlineExceeded: Pod was active on the node longer than the specified deadline"),
		Entry("phase only",
			corev1.PodStatus{Phase: corev1.PodFailed},
			"Failed"),
		Entry("several container statuses",
			corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{
				{Name: "first", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Message: "first failure"}}},
				{Name: "second", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 3, Message: "second failure"}}},
			}},
			"exit code 1: first failure; exit code 3: second failure"),
		Entry("multi-line message",
			corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "assessment",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Message: "boom\n\tat step 1\n  at step 2"}},
			}}},
			"exit code 1: boom at step 1 at step 2"),
		Entry("over-long message",
			corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "assessment",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Message: strings.Repeat("a", maxConditionMessageLength)}},
			}}},
			"exit code 1: "+strings.Repeat("a", maxConditionMessageLength-len("exit code 1: ")-3)+"..."),
	)
})
