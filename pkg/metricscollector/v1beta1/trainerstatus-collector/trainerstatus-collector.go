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
	"io"
	"net/http"
	"time"

	"k8s.io/klog/v2"

	v1beta1 "github.com/kubeflow/katib/pkg/apis/manager/v1beta1"
)

// TrainerStatus represents the trainerStatus field from TrainJob
type TrainerStatus struct {
	ProgressPercentage        int32    `json:"progressPercentage,omitempty"`
	EstimatedRemainingSeconds int32    `json:"estimatedRemainingSeconds,omitempty"`
	LastUpdatedTime           string   `json:"lastUpdatedTime,omitempty"`
	Metrics                   []Metric `json:"metrics,omitempty"`
}

// Metric represents a single metric from trainerStatus
type Metric struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TrainJobStatus represents the relevant parts of TrainJob status
type TrainJobStatus struct {
	TrainerStatus *TrainerStatus `json:"trainerStatus,omitempty"`
}

// TrainJob represents the relevant parts of TrainJob resource
type TrainJob struct {
	Status TrainJobStatus `json:"status,omitempty"`
}

// CollectorConfig holds configuration for the TrainerStatus collector
type CollectorConfig struct {
	TrainJobName      string
	Namespace         string
	MetricNames       []string
	PollInterval      time.Duration
	KubernetesAPIHost string
	BearerToken       string
	CACertPath        string
}

// CollectObservationLog fetches trainerStatus from TrainJob and converts to ObservationLog
func CollectObservationLog(config CollectorConfig) (*v1beta1.ObservationLog, error) {
	trainerStatus, err := getTrainerStatus(config)
	if err != nil {
		return nil, fmt.Errorf("failed to get trainer status: %w", err)
	}

	if trainerStatus == nil {
		klog.Info("TrainerStatus is not yet available")
		return &v1beta1.ObservationLog{MetricLogs: []*v1beta1.MetricLog{}}, nil
	}

	return convertToObservationLog(trainerStatus, config.MetricNames), nil
}

// getTrainerStatus fetches the trainerStatus from the TrainJob resource
func getTrainerStatus(config CollectorConfig) (*TrainerStatus, error) {
	url := fmt.Sprintf(
		"%s/apis/trainer.kubeflow.org/v1alpha1/namespaces/%s/trainjobs/%s",
		config.KubernetesAPIHost,
		config.Namespace,
		config.TrainJobName,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if config.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+config.BearerToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TrainJob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch TrainJob: status %d, body: %s", resp.StatusCode, string(body))
	}

	var trainJob TrainJob
	if err := json.NewDecoder(resp.Body).Decode(&trainJob); err != nil {
		return nil, fmt.Errorf("failed to decode TrainJob: %w", err)
	}

	return trainJob.Status.TrainerStatus, nil
}

// convertToObservationLog converts TrainerStatus to Katib's ObservationLog format
func convertToObservationLog(status *TrainerStatus, metricNames []string) *v1beta1.ObservationLog {
	mlogs := make([]*v1beta1.MetricLog, 0)

	timestamp := status.LastUpdatedTime
	if timestamp == "" {
		timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	// Create a map for quick lookup of requested metrics
	requestedMetrics := make(map[string]bool)
	for _, name := range metricNames {
		requestedMetrics[name] = true
	}

	// Convert trainerStatus metrics to MetricLogs
	for _, metric := range status.Metrics {
		// If metricNames is empty, collect all metrics
		// Otherwise, only collect requested metrics
		if len(metricNames) == 0 || requestedMetrics[metric.Name] {
			mlogs = append(mlogs, &v1beta1.MetricLog{
				TimeStamp: timestamp,
				Metric: &v1beta1.Metric{
					Name:  metric.Name,
					Value: metric.Value,
				},
			})
		}
	}

	// Add progress percentage as a metric
	if len(metricNames) == 0 || requestedMetrics["progress_percentage"] {
		mlogs = append(mlogs, &v1beta1.MetricLog{
			TimeStamp: timestamp,
			Metric: &v1beta1.Metric{
				Name:  "progress_percentage",
				Value: fmt.Sprintf("%d", status.ProgressPercentage),
			},
		})
	}

	// Add estimated remaining seconds as a metric
	if status.EstimatedRemainingSeconds > 0 {
		if len(metricNames) == 0 || requestedMetrics["estimated_remaining_seconds"] {
			mlogs = append(mlogs, &v1beta1.MetricLog{
				TimeStamp: timestamp,
				Metric: &v1beta1.Metric{
					Name:  "estimated_remaining_seconds",
					Value: fmt.Sprintf("%d", status.EstimatedRemainingSeconds),
				},
			})
		}
	}

	return &v1beta1.ObservationLog{
		MetricLogs: mlogs,
	}
}

// WatchAndCollect continuously watches TrainJob status and reports metrics
func WatchAndCollect(
	ctx context.Context,
	config CollectorConfig,
	reportFunc func(*v1beta1.ObservationLog) error,
) error {
	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	var lastProgressPercentage int32 = -1

	for {
		select {
		case <-ctx.Done():
			klog.Info("Context cancelled, stopping trainer status collection")
			return nil
		case <-ticker.C:
			trainerStatus, err := getTrainerStatus(config)
			if err != nil {
				klog.Warningf("Failed to get trainer status: %v", err)
				continue
			}

			if trainerStatus == nil {
				klog.V(4).Info("TrainerStatus not yet available")
				continue
			}

			// Only report if progress has changed
			if trainerStatus.ProgressPercentage != lastProgressPercentage {
				observationLog := convertToObservationLog(trainerStatus, config.MetricNames)

				if err := reportFunc(observationLog); err != nil {
					klog.Errorf("Failed to report observation log: %v", err)
				} else {
					klog.Infof("Reported metrics: progress=%d%%, metrics=%d",
						trainerStatus.ProgressPercentage, len(observationLog.MetricLogs))
				}

				lastProgressPercentage = trainerStatus.ProgressPercentage
			}

			// Check if training is complete
			if trainerStatus.ProgressPercentage >= 100 {
				klog.Info("Training complete (100%), final metrics reported")
				return nil
			}
		}
	}
}
