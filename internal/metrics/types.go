// Copyright (c) Tailscale Gateway Authors
// SPDX-License-Identifier: BSD-3-Clause

package metrics

import (
	"time"
)

// ReconcilerMetrics tracks reconciliation performance across all controllers
// This type is shared to ensure consistent metrics collection across the operator
type ReconcilerMetrics struct {
	ReconcileCount       int64
	SuccessfulReconciles int64
	FailedReconciles     int64
	APICallCount         int64
	LastReconcileTime    time.Time
	TotalReconcileTime   time.Duration
}

// RecordReconcile records a successful reconciliation
func (m *ReconcilerMetrics) RecordReconcile(duration time.Duration) {
	m.ReconcileCount++
	m.SuccessfulReconciles++
	m.LastReconcileTime = time.Now()
	m.TotalReconcileTime += duration
}

// RecordFailure records a failed reconciliation
func (m *ReconcilerMetrics) RecordFailure() {
	m.ReconcileCount++
	m.FailedReconciles++
	m.LastReconcileTime = time.Now()
}

// RecordAPICall records an API call
func (m *ReconcilerMetrics) RecordAPICall() {
	m.APICallCount++
}

// AverageReconcileTime returns the average reconciliation time
func (m *ReconcilerMetrics) AverageReconcileTime() time.Duration {
	if m.SuccessfulReconciles == 0 {
		return 0
	}
	return m.TotalReconcileTime / time.Duration(m.SuccessfulReconciles)
}

// ErrorRate returns the error rate as a percentage
func (m *ReconcilerMetrics) ErrorRate() float64 {
	if m.ReconcileCount == 0 {
		return 0
	}
	return float64(m.FailedReconciles) / float64(m.ReconcileCount) * 100
}
