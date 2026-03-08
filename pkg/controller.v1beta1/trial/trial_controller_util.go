/*
Copyright 2022 The Kubeflow Authors.

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

package trial

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1beta1 "github.com/kubeflow/katib/pkg/apis/controller/common/v1beta1"
	trialsv1beta1 "github.com/kubeflow/katib/pkg/apis/controller/trials/v1beta1"
	api_pb "github.com/kubeflow/katib/pkg/apis/manager/v1beta1"
	"github.com/kubeflow/katib/pkg/controller.v1beta1/consts"
	trialutil "github.com/kubeflow/katib/pkg/controller.v1beta1/trial/util"
)

const (
	cleanMetricsFinalizer = "clean-metrics-in-db"
)

// UpdateTrialStatusCondition updates Trial status from current deployed Job status
func (r *ReconcileTrial) UpdateTrialStatusCondition(instance *trialsv1beta1.Trial, deployedJobName string, jobStatus *trialutil.TrialJobStatus) error {
	logger := log.WithValues("Trial", types.NamespacedName{Name: instance.GetName(), Namespace: instance.GetNamespace()})

	timeNow := metav1.Now()

	if jobStatus.Condition == trialutil.JobSucceeded {
		if instance.IsObservationAvailable() && !instance.IsSucceeded() {
			if !instance.IsEarlyStopped() {
				msg := "Trial has succeeded"
				reason := TrialSucceededReason

				// Get message and reason from deployed job
				if jobStatus.Message != "" {
					msg = fmt.Sprintf("%v. Job message: %v", msg, jobStatus.Message)
				}
				if jobStatus.Reason != "" {
					reason = fmt.Sprintf("%v. Job reason: %v", reason, jobStatus.Reason)
				}

				logger.Info("Trial status changed to Succeeded")
				instance.MarkTrialStatusSucceeded(corev1.ConditionTrue, reason, msg)
				instance.Status.CompletionTime = &timeNow

				eventMsg := fmt.Sprintf("Job %v has succeeded", deployedJobName)
				r.recorder.Eventf(instance, corev1.EventTypeNormal, JobSucceededReason, eventMsg)
				r.collector.IncreaseTrialsSucceededCount(instance.Namespace)
			}
		} else if !instance.IsMetricsUnavailable() {
			msg := "Metrics are not available"
			reason := TrialMetricsUnavailableReason

			// If the type of metrics collector is Push, We should insert an unavailable value to Katib DB.
			// We would retry reconciliation if some error occurs while we report unavailable metrics.
			if instance.Spec.MetricsCollector.Collector.Kind == commonv1beta1.PushCollector {
				if err := r.reportUnavailableMetrics(instance); err != nil {
					logger.Error(err, "Failed to insert unavailable value to Katib DB")
					return fmt.Errorf("%w: %w", errReportMetricsFailed, err)
				}
			}

			// Get message and reason from deployed job
			if jobStatus.Message != "" {
				msg = fmt.Sprintf("%v. Job message: %v", msg, jobStatus.Message)
			}
			if jobStatus.Reason != "" {
				reason = fmt.Sprintf("%v. Job reason: %v", reason, jobStatus.Reason)
			}

			logger.Info("Trial status changed to Metrics Unavailable")
			instance.MarkTrialStatusMetricsUnavailable(reason, msg)
			instance.Status.CompletionTime = &timeNow

			eventMsg := fmt.Sprintf("Metrics are not available for Job %v", deployedJobName)
			r.recorder.Eventf(instance, corev1.EventTypeWarning, JobMetricsUnavailableReason, eventMsg)
			r.collector.IncreaseTrialsMetricsUnavailableCount(instance.Namespace)
		}
	} else if jobStatus.Condition == trialutil.JobFailed && !instance.IsFailed() && !instance.IsEarlyStopped() {
		msg := "Trial has failed"
		reason := TrialFailedReason

		// Get message and reason from deployed job
		if jobStatus.Message != "" {
			msg = fmt.Sprintf("%v. Job message: %v", msg, jobStatus.Message)
		}
		if jobStatus.Reason != "" {
			reason = fmt.Sprintf("%v. Job reason: %v", reason, jobStatus.Reason)
		}

		instance.MarkTrialStatusFailed(reason, msg)
		instance.Status.CompletionTime = &timeNow

		eventMsg := fmt.Sprintf("Job %v has failed", deployedJobName)
		if jobStatus.Message != "" || jobStatus.Reason != "" {
			eventMsg = fmt.Sprintf("%v. %v %v", eventMsg, jobStatus.Message, jobStatus.Reason)
		}

		r.recorder.Eventf(instance, corev1.EventTypeNormal, JobFailedReason, eventMsg)
		r.collector.IncreaseTrialsFailedCount(instance.Namespace)
		logger.Info("Trial status changed to Failed")
	} else if jobStatus.Condition == trialutil.JobRunning && !instance.IsRunning() && !instance.IsEarlyStopped() {
		msg := "Trial is running"
		instance.MarkTrialStatusRunning(TrialRunningReason, msg)

		eventMsg := fmt.Sprintf("Job %v is running", deployedJobName)
		r.recorder.Eventf(instance, corev1.EventTypeNormal, JobRunningReason, eventMsg)
		logger.Info("Trial status changed to Running")
		// TODO(gaocegege): Should we maintain a TrialsRunningCount?
	}
	// else nothing to do
	return nil
}

func (r *ReconcileTrial) UpdateTrialStatusObservation(instance *trialsv1beta1.Trial) error {
	reply, err := r.GetTrialObservationLog(instance)
	if err != nil {
		log.Error(err, "Get trial observation log error")
		return err
	}
	metricStrategies := instance.Spec.Objective.MetricStrategies
	if len(reply.ObservationLog.MetricLogs) != 0 {
		observation, err := getMetrics(reply.ObservationLog.MetricLogs, metricStrategies)
		if err != nil {
			log.Error(err, "Get metrics from logs error")
			return err
		}
		instance.Status.Observation = observation

		// Extract training progress if using TrainerStatus collector (legacy DB-based approach)
		if instance.Spec.MetricsCollector.Collector.Kind == commonv1beta1.TrainerStatusCollector {
			trainingProgress := getTrainingProgress(reply.ObservationLog.MetricLogs)
			if trainingProgress != nil {
				instance.Status.TrainingProgress = trainingProgress
			}
		}
	}
	return nil
}

// reportObservationToDB reports final observation metrics to Katib DB for historical tracking.
// Used by TrainerStatusCollector to store final metrics when trial completes.
func (r *ReconcileTrial) reportObservationToDB(instance *trialsv1beta1.Trial, observation *commonv1beta1.Observation) error {
	if observation == nil || len(observation.Metrics) == 0 {
		return nil
	}

	// Convert observation to ObservationLog format
	observationLog := &api_pb.ObservationLog{
		MetricLogs: make([]*api_pb.MetricLog, 0, len(observation.Metrics)),
	}

	timestamp := time.Now().UTC().Format(time.RFC3339)
	for _, metric := range observation.Metrics {
		observationLog.MetricLogs = append(observationLog.MetricLogs, &api_pb.MetricLog{
			TimeStamp: timestamp,
			Metric: &api_pb.Metric{
				Name:  metric.Name,
				Value: metric.Latest,
			},
		})
	}

	// Report to DB Manager
	_, err := r.ReportTrialObservationLog(instance, observationLog)
	if err != nil {
		return fmt.Errorf("failed to report observation log to DB: %w", err)
	}

	log.Info("Reported final observation to DB", "Trial", instance.Name, "Metrics", len(observation.Metrics))
	return nil
}

// getTrainingProgressFromTrainJob extracts TrainingProgress directly from TrainJob.status.trainerStatus.
// This is the direct read approach - no sidecar or DB needed during training.
// existingObs is used to maintain historical min/max values across updates.
func getTrainingProgressFromTrainJob(deployedJob *unstructured.Unstructured, existingObs *commonv1beta1.Observation) (*commonv1beta1.TrainingProgress, *commonv1beta1.Observation) {
	if deployedJob == nil {
		return nil, nil
	}

	// Only process TrainJob resources
	if deployedJob.GetKind() != "TrainJob" {
		return nil, nil
	}

	// Extract status.trainerStatus using unstructured
	status, found, err := unstructured.NestedMap(deployedJob.Object, "status", "trainerStatus")
	if err != nil || !found || status == nil {
		return nil, nil
	}

	progress := &commonv1beta1.TrainingProgress{}

	// Extract progressPercentage
	if val, ok := status["progressPercentage"]; ok {
		switch v := val.(type) {
		case int64:
			progress.ProgressPercentage = int32(v)
		case float64:
			progress.ProgressPercentage = int32(v)
		}
	}

	// Extract estimatedRemainingSeconds
	if val, ok := status["estimatedRemainingSeconds"]; ok {
		switch v := val.(type) {
		case int64:
			progress.EstimatedRemainingSeconds = v
		case float64:
			progress.EstimatedRemainingSeconds = int64(v)
		}
	}

	// Extract lastUpdatedTime
	if val, ok := status["lastUpdatedTime"].(string); ok {
		progress.LastUpdatedTime = val
	}

	// Build map of existing metrics for min/max tracking
	existingMetrics := make(map[string]*commonv1beta1.Metric)
	if existingObs != nil {
		for i := range existingObs.Metrics {
			existingMetrics[existingObs.Metrics[i].Name] = &existingObs.Metrics[i]
		}
	}

	// Extract metrics from trainerStatus.metrics
	var observation *commonv1beta1.Observation
	if metricsRaw, ok := status["metrics"].([]interface{}); ok && len(metricsRaw) > 0 {
		observation = &commonv1beta1.Observation{
			Metrics: []commonv1beta1.Metric{},
		}
		for _, m := range metricsRaw {
			if metricMap, ok := m.(map[string]interface{}); ok {
				name, _ := metricMap["name"].(string)
				value, _ := metricMap["value"].(string)
				if name != "" && value != "" {
					// Add to progress.CurrentMetrics
					progress.CurrentMetrics = append(progress.CurrentMetrics, commonv1beta1.Metric{
						Name:   name,
						Latest: value,
					})

					// Update observation with min/max tracking
					metric := commonv1beta1.Metric{
						Name:   name,
						Latest: value,
						Min:    value,
						Max:    value,
					}

					// Merge with existing min/max if available
					if existing, ok := existingMetrics[name]; ok {
						metric.Min = minMetricValue(existing.Min, value)
						metric.Max = maxMetricValue(existing.Max, value)
					}

					observation.Metrics = append(observation.Metrics, metric)
				}
			}
		}
	}

	return progress, observation
}

// minMetricValue returns the smaller of two metric values (as strings representing floats)
func minMetricValue(a, b string) string {
	aVal, aErr := strconv.ParseFloat(a, 64)
	bVal, bErr := strconv.ParseFloat(b, 64)
	if aErr != nil {
		return b
	}
	if bErr != nil {
		return a
	}
	if aVal < bVal {
		return a
	}
	return b
}

// maxMetricValue returns the larger of two metric values (as strings representing floats)
func maxMetricValue(a, b string) string {
	aVal, aErr := strconv.ParseFloat(a, 64)
	bVal, bErr := strconv.ParseFloat(b, 64)
	if aErr != nil {
		return b
	}
	if bErr != nil {
		return a
	}
	if aVal > bVal {
		return a
	}
	return b
}

// getTrainingProgress extracts training progress from metrics reported by TrainerStatus collector (DB-based, legacy)
func getTrainingProgress(metricLogs []*api_pb.MetricLog) *commonv1beta1.TrainingProgress {
	progress := &commonv1beta1.TrainingProgress{}
	var latestTimestamp *time.Time
	currentMetrics := make(map[string]string)

	for _, metricLog := range metricLogs {
		metricName := metricLog.Metric.Name
		metricValue := metricLog.Metric.Value

		// Parse timestamp to find the latest metrics
		logTime, err := time.Parse(time.RFC3339Nano, metricLog.TimeStamp)
		if err != nil {
			continue
		}

		// Track latest timestamp
		if latestTimestamp == nil || logTime.After(*latestTimestamp) {
			latestTimestamp = &logTime
			progress.LastUpdatedTime = metricLog.TimeStamp
		}

		// Extract progress-specific metrics
		switch metricName {
		case "progress_percentage":
			if val, err := strconv.ParseInt(metricValue, 10, 32); err == nil {
				progress.ProgressPercentage = int32(val)
			}
		case "estimated_remaining_seconds":
			if val, err := strconv.ParseInt(metricValue, 10, 64); err == nil {
				progress.EstimatedRemainingSeconds = val
			}
		case "current_step":
			if val, err := strconv.ParseInt(metricValue, 10, 32); err == nil {
				progress.CurrentStep = int32(val)
			}
		case "total_steps":
			if val, err := strconv.ParseInt(metricValue, 10, 32); err == nil {
				progress.TotalSteps = int32(val)
			}
		default:
			// Store other metrics as current metrics
			currentMetrics[metricName] = metricValue
		}
	}

	// Convert current metrics to slice
	for name, value := range currentMetrics {
		progress.CurrentMetrics = append(progress.CurrentMetrics, commonv1beta1.Metric{
			Name:   name,
			Latest: value,
		})
	}

	// Return progress if we have any data (including 0% progress)
	// Check for meaningful data: timestamp set, metrics collected, or explicitly set progress
	if progress.LastUpdatedTime != "" || len(progress.CurrentMetrics) > 0 || progress.TotalSteps > 0 {
		return progress
	}
	return nil
}

func (r *ReconcileTrial) updateFinalizers(instance *trialsv1beta1.Trial, finalizers []string) (reconcile.Result, error) {
	isDelete := true
	if !instance.ObjectMeta.DeletionTimestamp.IsZero() {
		if _, err := r.DeleteTrialObservationLog(instance); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		isDelete = false
	}
	instance.SetFinalizers(finalizers)
	if err := r.Update(context.TODO(), instance); err != nil {
		return reconcile.Result{}, err
	} else {
		if isDelete {
			r.collector.IncreaseTrialsDeletedCount(instance.Namespace)
		} else {
			r.collector.IncreaseTrialsCreatedCount(instance.Namespace)
		}
		// Need to requeue because finalizer update does not change metadata.generation
		return reconcile.Result{Requeue: true}, err
	}
}

func (r *ReconcileTrial) reportUnavailableMetrics(instance *trialsv1beta1.Trial) error {
	observationLog := &api_pb.ObservationLog{
		MetricLogs: []*api_pb.MetricLog{
			{
				TimeStamp: time.Time{}.UTC().Format(time.RFC3339),
				Metric: &api_pb.Metric{
					Name:  instance.Spec.Objective.ObjectiveMetricName,
					Value: consts.UnavailableMetricValue,
				},
			},
		},
	}
	_, err := r.ReportTrialObservationLog(instance, observationLog)

	return err
}

func getMetrics(metricLogs []*api_pb.MetricLog, strategies []commonv1beta1.MetricStrategy) (*commonv1beta1.Observation, error) {
	metrics := make(map[string]*commonv1beta1.Metric)
	timestamps := make(map[string]*time.Time)
	for _, strategy := range strategies {
		timestamps[strategy.Name] = nil
		metrics[strategy.Name] = &commonv1beta1.Metric{
			Name:   strategy.Name,
			Min:    consts.UnavailableMetricValue,
			Max:    consts.UnavailableMetricValue,
			Latest: consts.UnavailableMetricValue,
		}
	}

	for _, metricLog := range metricLogs {
		metric, ok := metrics[metricLog.Metric.Name]
		if !ok {
			continue
		}
		strValue := metricLog.Metric.Value
		floatValue, err := strconv.ParseFloat(strValue, 64)
		if err == nil {
			if metric.Min == consts.UnavailableMetricValue {
				metric.Min = strValue
				metric.Max = strValue
			} else {
				// We can't get error here, because we parsed this value before
				minMetric, _ := strconv.ParseFloat(metric.Min, 64)
				maxMetric, _ := strconv.ParseFloat(metric.Max, 64)
				if floatValue < minMetric {
					metric.Min = strValue
				} else if floatValue > maxMetric {
					metric.Max = strValue
				}
			}
		}
		currentTime, err := time.Parse(time.RFC3339Nano, metricLog.TimeStamp)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamps %s: %e", metricLog.TimeStamp, err)
		}
		timestamp := timestamps[metricLog.Metric.Name]
		if timestamp == nil || !timestamp.After(currentTime) {
			timestamps[metricLog.Metric.Name] = &currentTime
			metric.Latest = strValue
		}
	}

	observation := &commonv1beta1.Observation{}
	for _, metric := range metrics {
		observation.Metrics = append(observation.Metrics, *metric)
	}

	return observation, nil
}

func needUpdateFinalizers(trial *trialsv1beta1.Trial) (bool, []string) {
	deleted := !trial.ObjectMeta.DeletionTimestamp.IsZero()
	pendingFinalizers := trial.GetFinalizers()
	contained := false
	for _, elem := range pendingFinalizers {
		if elem == cleanMetricsFinalizer {
			contained = true
			break
		}
	}

	if !deleted && !contained {
		finalizers := append(pendingFinalizers, cleanMetricsFinalizer)
		return true, finalizers
	}
	if deleted && contained {
		finalizers := []string{}
		for _, pendingFinalizer := range pendingFinalizers {
			if pendingFinalizer != cleanMetricsFinalizer {
				finalizers = append(finalizers, pendingFinalizer)
			}
		}
		return true, finalizers
	}
	return false, []string{}
}
