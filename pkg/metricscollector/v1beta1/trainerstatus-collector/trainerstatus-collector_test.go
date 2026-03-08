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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	v1beta1 "github.com/kubeflow/katib/pkg/apis/manager/v1beta1"
)

func TestConvertToObservationLog(t *testing.T) {
	testCases := map[string]struct {
		trainerStatus *TrainerStatus
		metricNames   []string
		expected      *v1beta1.ObservationLog
	}{
		"Full trainer status with all metrics": {
			trainerStatus: &TrainerStatus{
				ProgressPercentage:        45,
				EstimatedRemainingSeconds: 120,
				LastUpdatedTime:           "2024-03-04T17:55:08Z",
				Metrics: []Metric{
					{Name: "loss", Value: "0.234"},
					{Name: "accuracy", Value: "0.876"},
				},
			},
			metricNames: []string{},
			expected: &v1beta1.ObservationLog{
				MetricLogs: []*v1beta1.MetricLog{
					{TimeStamp: "2024-03-04T17:55:08Z", Metric: &v1beta1.Metric{Name: "loss", Value: "0.234"}},
					{TimeStamp: "2024-03-04T17:55:08Z", Metric: &v1beta1.Metric{Name: "accuracy", Value: "0.876"}},
					{TimeStamp: "2024-03-04T17:55:08Z", Metric: &v1beta1.Metric{Name: "progress_percentage", Value: "45"}},
					{TimeStamp: "2024-03-04T17:55:08Z", Metric: &v1beta1.Metric{Name: "estimated_remaining_seconds", Value: "120"}},
				},
			},
		},
		"Filter specific metrics": {
			trainerStatus: &TrainerStatus{
				ProgressPercentage:        50,
				EstimatedRemainingSeconds: 60,
				LastUpdatedTime:           "2024-03-04T18:00:00Z",
				Metrics: []Metric{
					{Name: "loss", Value: "0.123"},
					{Name: "accuracy", Value: "0.912"},
					{Name: "learning_rate", Value: "0.001"},
				},
			},
			metricNames: []string{"loss", "accuracy"},
			expected: &v1beta1.ObservationLog{
				MetricLogs: []*v1beta1.MetricLog{
					{TimeStamp: "2024-03-04T18:00:00Z", Metric: &v1beta1.Metric{Name: "loss", Value: "0.123"}},
					{TimeStamp: "2024-03-04T18:00:00Z", Metric: &v1beta1.Metric{Name: "accuracy", Value: "0.912"}},
				},
			},
		},
		"No estimated remaining time": {
			trainerStatus: &TrainerStatus{
				ProgressPercentage:        100,
				EstimatedRemainingSeconds: 0,
				LastUpdatedTime:           "2024-03-04T19:00:00Z",
				Metrics: []Metric{
					{Name: "loss", Value: "0.05"},
				},
			},
			metricNames: []string{},
			expected: &v1beta1.ObservationLog{
				MetricLogs: []*v1beta1.MetricLog{
					{TimeStamp: "2024-03-04T19:00:00Z", Metric: &v1beta1.Metric{Name: "loss", Value: "0.05"}},
					{TimeStamp: "2024-03-04T19:00:00Z", Metric: &v1beta1.Metric{Name: "progress_percentage", Value: "100"}},
				},
			},
		},
		"Empty metrics list": {
			trainerStatus: &TrainerStatus{
				ProgressPercentage: 25,
				LastUpdatedTime:    "2024-03-04T16:00:00Z",
				Metrics:            []Metric{},
			},
			metricNames: []string{},
			expected: &v1beta1.ObservationLog{
				MetricLogs: []*v1beta1.MetricLog{
					{TimeStamp: "2024-03-04T16:00:00Z", Metric: &v1beta1.Metric{Name: "progress_percentage", Value: "25"}},
				},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			actual := convertToObservationLog(tc.trainerStatus, tc.metricNames)

			opts := cmpopts.IgnoreUnexported(v1beta1.ObservationLog{}, v1beta1.MetricLog{}, v1beta1.Metric{})
			if diff := cmp.Diff(tc.expected, actual, opts); diff != "" {
				t.Errorf("Unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCollectObservationLog(t *testing.T) {
	testCases := map[string]struct {
		serverResponse interface{}
		statusCode     int
		metricNames    []string
		wantError      bool
		expectedLogs   int
	}{
		"Valid TrainJob with trainerStatus": {
			serverResponse: TrainJob{
				Status: TrainJobStatus{
					TrainerStatus: &TrainerStatus{
						ProgressPercentage:        75,
						EstimatedRemainingSeconds: 30,
						LastUpdatedTime:           "2024-03-04T17:55:08Z",
						Metrics: []Metric{
							{Name: "loss", Value: "0.1"},
						},
					},
				},
			},
			statusCode:   http.StatusOK,
			metricNames:  []string{},
			wantError:    false,
			expectedLogs: 3, // loss, progress_percentage, estimated_remaining_seconds
		},
		"TrainJob without trainerStatus": {
			serverResponse: TrainJob{
				Status: TrainJobStatus{
					TrainerStatus: nil,
				},
			},
			statusCode:   http.StatusOK,
			metricNames:  []string{},
			wantError:    false,
			expectedLogs: 0,
		},
		"Server returns 404": {
			serverResponse: map[string]string{"error": "not found"},
			statusCode:     http.StatusNotFound,
			metricNames:    []string{},
			wantError:      true,
			expectedLogs:   0,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				json.NewEncoder(w).Encode(tc.serverResponse)
			}))
			defer server.Close()

			config := CollectorConfig{
				TrainJobName:      "test-job",
				Namespace:         "test-ns",
				MetricNames:       tc.metricNames,
				PollInterval:      time.Second,
				KubernetesAPIHost: server.URL,
				BearerToken:       "",
			}

			result, err := CollectObservationLog(config)

			if tc.wantError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result.MetricLogs) != tc.expectedLogs {
				t.Errorf("Expected %d metric logs, got %d", tc.expectedLogs, len(result.MetricLogs))
			}
		})
	}
}

func TestWatchAndCollect(t *testing.T) {
	callCount := 0
	progressValues := []int32{25, 50, 75, 100}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		progress := progressValues[0]
		if callCount < len(progressValues) {
			progress = progressValues[callCount]
			callCount++
		}

		response := TrainJob{
			Status: TrainJobStatus{
				TrainerStatus: &TrainerStatus{
					ProgressPercentage: progress,
					LastUpdatedTime:    "2024-03-04T17:55:08Z",
					Metrics: []Metric{
						{Name: "loss", Value: "0.1"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := CollectorConfig{
		TrainJobName:      "test-job",
		Namespace:         "test-ns",
		MetricNames:       []string{},
		PollInterval:      10 * time.Millisecond,
		KubernetesAPIHost: server.URL,
	}

	reportedLogs := []*v1beta1.ObservationLog{}
	reportFunc := func(log *v1beta1.ObservationLog) error {
		reportedLogs = append(reportedLogs, log)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := WatchAndCollect(ctx, config, reportFunc)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Should have reported at least once for each progress change
	if len(reportedLogs) < 2 {
		t.Errorf("Expected at least 2 reports, got %d", len(reportedLogs))
	}
}
