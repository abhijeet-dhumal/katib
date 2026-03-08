//go:build integration

/*
Copyright 2024 The Kubeflow Authors.

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

package trainerstatuscollector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	v1beta1 "github.com/kubeflow/katib/pkg/apis/manager/v1beta1"
)

// TestIntegrationWatchAndCollectWithProgressUpdates simulates a realistic training scenario
// where progress updates are received over time.
func TestIntegrationWatchAndCollectWithProgressUpdates(t *testing.T) {
	var (
		mu              sync.Mutex
		currentProgress int32 = 0
		currentStep     int32 = 0
		totalSteps      int32 = 100
		loss            float64 = 1.0
	)

	// Simulate a TrainJob that updates progress over time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		// Simulate progress updates
		if currentProgress < 100 {
			currentStep += 10
			currentProgress = int32(float64(currentStep) / float64(totalSteps) * 100)
			loss -= 0.1
		}

		remaining := int64(0)
		if currentProgress < 100 {
			remaining = int64((100 - currentProgress) / 10 * 2) // 2 seconds per 10%
		}

		response := TrainJob{
			Status: TrainJobStatus{
				TrainerStatus: &TrainerStatus{
					ProgressPercentage:        currentProgress,
					EstimatedRemainingSeconds: remaining,
					LastUpdatedTime:           time.Now().UTC().Format(time.RFC3339),
					Metrics: []Metric{
						{Name: "loss", Value: fmt.Sprintf("%.4f", loss)},
						{Name: "current_step", Value: fmt.Sprintf("%d", currentStep)},
						{Name: "total_steps", Value: fmt.Sprintf("%d", totalSteps)},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := CollectorConfig{
		TrainJobName:      "integration-test-job",
		Namespace:         "test-ns",
		MetricNames:       []string{}, // Collect all metrics
		PollInterval:      50 * time.Millisecond,
		KubernetesAPIHost: server.URL,
	}

	var (
		reportedLogs      []*v1beta1.ObservationLog
		progressValues    []int32
		reportMu          sync.Mutex
	)

	reportFunc := func(log *v1beta1.ObservationLog) error {
		reportMu.Lock()
		defer reportMu.Unlock()
		reportedLogs = append(reportedLogs, log)

		// Extract progress percentage
		for _, ml := range log.MetricLogs {
			if ml.Metric.Name == "progress_percentage" {
				var p int32
				fmt.Sscanf(ml.Metric.Value, "%d", &p)
				progressValues = append(progressValues, p)
			}
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := WatchAndCollect(ctx, config, reportFunc)
	if err != nil {
		t.Errorf("WatchAndCollect returned error: %v", err)
	}

	// Verify we received progress updates
	reportMu.Lock()
	defer reportMu.Unlock()

	if len(reportedLogs) < 5 {
		t.Errorf("Expected at least 5 progress reports, got %d", len(reportedLogs))
	}

	// Verify progress increased monotonically
	for i := 1; i < len(progressValues); i++ {
		if progressValues[i] < progressValues[i-1] {
			t.Errorf("Progress decreased: %d -> %d", progressValues[i-1], progressValues[i])
		}
	}

	// Verify final progress reached 100%
	if len(progressValues) > 0 && progressValues[len(progressValues)-1] != 100 {
		t.Errorf("Expected final progress to be 100%%, got %d%%", progressValues[len(progressValues)-1])
	}

	// Verify metrics are present
	lastLog := reportedLogs[len(reportedLogs)-1]
	hasLoss := false
	hasCurrentStep := false
	hasTotalSteps := false
	hasProgress := false

	for _, ml := range lastLog.MetricLogs {
		switch ml.Metric.Name {
		case "loss":
			hasLoss = true
		case "current_step":
			hasCurrentStep = true
		case "total_steps":
			hasTotalSteps = true
		case "progress_percentage":
			hasProgress = true
		}
	}

	if !hasLoss {
		t.Error("Missing 'loss' metric")
	}
	if !hasCurrentStep {
		t.Error("Missing 'current_step' metric")
	}
	if !hasTotalSteps {
		t.Error("Missing 'total_steps' metric")
	}
	if !hasProgress {
		t.Error("Missing 'progress_percentage' metric")
	}

	t.Logf("Integration test passed: %d progress reports, final progress %d%%",
		len(reportedLogs), progressValues[len(progressValues)-1])
}

// TestIntegrationCollectorWithDelayedTrainerStatus tests the scenario where
// trainerStatus is not immediately available.
func TestIntegrationCollectorWithDelayedTrainerStatus(t *testing.T) {
	callCount := 0
	delayUntilCall := 3 // trainerStatus becomes available after 3 calls

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		var response TrainJob
		if callCount < delayUntilCall {
			// No trainerStatus yet
			response = TrainJob{
				Status: TrainJobStatus{
					TrainerStatus: nil,
				},
			}
		} else {
			// trainerStatus becomes available
			response = TrainJob{
				Status: TrainJobStatus{
					TrainerStatus: &TrainerStatus{
						ProgressPercentage:        100,
						EstimatedRemainingSeconds: 0,
						LastUpdatedTime:           time.Now().UTC().Format(time.RFC3339),
						Metrics: []Metric{
							{Name: "loss", Value: "0.05"},
						},
					},
				},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := CollectorConfig{
		TrainJobName:      "delayed-status-job",
		Namespace:         "test-ns",
		MetricNames:       []string{},
		PollInterval:      20 * time.Millisecond,
		KubernetesAPIHost: server.URL,
	}

	reportCount := 0
	reportFunc := func(log *v1beta1.ObservationLog) error {
		reportCount++
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := WatchAndCollect(ctx, config, reportFunc)
	if err != nil {
		t.Errorf("WatchAndCollect returned error: %v", err)
	}

	// Should have made at least delayUntilCall API calls
	if callCount < delayUntilCall {
		t.Errorf("Expected at least %d API calls, got %d", delayUntilCall, callCount)
	}

	// Should have reported once when trainerStatus became available
	if reportCount < 1 {
		t.Errorf("Expected at least 1 report after trainerStatus became available, got %d", reportCount)
	}

	t.Logf("Delayed status test passed: %d API calls, %d reports", callCount, reportCount)
}

// TestIntegrationMetricFiltering tests that metric filtering works correctly.
func TestIntegrationMetricFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := TrainJob{
			Status: TrainJobStatus{
				TrainerStatus: &TrainerStatus{
					ProgressPercentage:        100,
					EstimatedRemainingSeconds: 0,
					LastUpdatedTime:           time.Now().UTC().Format(time.RFC3339),
					Metrics: []Metric{
						{Name: "loss", Value: "0.05"},
						{Name: "accuracy", Value: "0.95"},
						{Name: "learning_rate", Value: "0.001"},
						{Name: "epoch", Value: "10"},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Only collect loss and accuracy
	config := CollectorConfig{
		TrainJobName:      "filter-test-job",
		Namespace:         "test-ns",
		MetricNames:       []string{"loss", "accuracy"},
		PollInterval:      20 * time.Millisecond,
		KubernetesAPIHost: server.URL,
	}

	var lastLog *v1beta1.ObservationLog
	reportFunc := func(log *v1beta1.ObservationLog) error {
		lastLog = log
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	WatchAndCollect(ctx, config, reportFunc)

	if lastLog == nil {
		t.Fatal("No logs reported")
	}

	// Check that only filtered metrics are present
	metricNames := make(map[string]bool)
	for _, ml := range lastLog.MetricLogs {
		metricNames[ml.Metric.Name] = true
	}

	if !metricNames["loss"] {
		t.Error("Expected 'loss' metric to be collected")
	}
	if !metricNames["accuracy"] {
		t.Error("Expected 'accuracy' metric to be collected")
	}
	if metricNames["learning_rate"] {
		t.Error("'learning_rate' should have been filtered out")
	}
	if metricNames["epoch"] {
		t.Error("'epoch' should have been filtered out")
	}

	t.Logf("Metric filtering test passed: collected metrics %v", metricNames)
}
