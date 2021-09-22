package util

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The default durations for the leader election operations.
var (
	// LeaseDuration is the default duration for the leader election lease.
	LeaseDuration = metav1.Duration{Duration: 137 * time.Second}
	// RenewDeadline is the default duration for the leader renewal.
	RenewDeadline = metav1.Duration{Duration: 107 * time.Second}
	// RetryPeriod is the default duration for the leader election retrial.
	RetryPeriod = metav1.Duration{Duration: 26 * time.Second}
)
