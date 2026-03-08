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
	"errors"
	"fmt"
	"time"

	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	commonapiv1beta1 "github.com/kubeflow/katib/pkg/apis/controller/common/v1beta1"
	trialsv1beta1 "github.com/kubeflow/katib/pkg/apis/controller/trials/v1beta1"
	"github.com/kubeflow/katib/pkg/controller.v1beta1/consts"
	"github.com/kubeflow/katib/pkg/controller.v1beta1/trial/managerclient"
	trialutil "github.com/kubeflow/katib/pkg/controller.v1beta1/trial/util"
)

const (
	// ControllerName is the controller name.
	ControllerName = "trial-controller"
)

var (
	log = logf.Log.WithName(ControllerName)
	// errMetricsNotReported is the error when Trial job is succeeded but metrics are not reported yet
	errMetricsNotReported = fmt.Errorf("metrics are not reported yet")
	// errReportMetricsFailed is the error when `unavailable` metrics value can't be inserted to the Katib DB.
	errReportMetricsFailed = fmt.Errorf("failed to report unavailable metrics")
	// errTrainerStatusPolling triggers requeue for real-time progress updates
	errTrainerStatusPolling = fmt.Errorf("polling TrainJob status for real-time progress")
)

// Add creates a new Trial Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	r := &ReconcileTrial{
		Client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		ManagerClient: managerclient.New(),
		recorder:      mgr.GetEventRecorderFor(ControllerName),
		collector:     trialutil.NewTrialsCollector(mgr.GetCache(), metrics.Registry),
	}
	r.updateStatusHandler = r.updateStatus
	return r
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("trial-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		log.Error(err, "Create trial controller error")
		return err
	}

	// Watch for changes in Trial
	if err = c.Watch(source.Kind[*trialsv1beta1.Trial](
		mgr.GetCache(), &trialsv1beta1.Trial{},
		&handler.TypedEnqueueRequestForObject[*trialsv1beta1.Trial]{},
	)); err != nil {
		log.Error(err, "Trial watch error")
		return err
	}

	trialResources := viper.Get(consts.ConfigTrialResources)
	if trialResources != nil {
		// Cast interface to gvk slice object
		gvkList := trialResources.([]schema.GroupVersionKind)

		// Watch for changes in custom resources
		for _, gvk := range gvkList {
			// Check if CRD is installed on the cluster.
			_, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				if meta.IsNoMatchError(err) {
					log.Info("Job watch error. CRD might be missing. Please install CRD and restart katib-controller",
						"CRD Group", gvk.Group, "CRD Version", gvk.Version, "CRD Kind", gvk.Kind)
					continue
				}
				return err
			}
			// Watch for the CRD changes.
			unstructuredJob := &unstructured.Unstructured{}
			unstructuredJob.SetGroupVersionKind(gvk)
			if err = c.Watch(source.Kind[*unstructured.Unstructured](
				mgr.GetCache(), unstructuredJob,
				handler.TypedEnqueueRequestForOwner[*unstructured.Unstructured](
					mgr.GetScheme(), mgr.GetRESTMapper(), &trialsv1beta1.Trial{}, handler.OnlyControllerOwner(),
				),
			)); err != nil {
				return err
			}
			log.Info("Job watch added successfully",
				"CRD Group", gvk.Group, "CRD Version", gvk.Version, "CRD Kind", gvk.Kind)
		}
	}
	log.Info("Trial controller created")
	return nil
}

var _ reconcile.Reconciler = &ReconcileTrial{}

// ReconcileTrial reconciles a Trial object
type ReconcileTrial struct {
	client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder

	managerclient.ManagerClient
	// updateStatusHandler is defined for test purpose.
	updateStatusHandler updateStatusFunc
	// collector is a wrapper for experiment metrics.
	collector *trialutil.TrialsCollector
}

// Reconcile reads that state of the cluster for a Trial object and makes changes based on the state read
// and what is in the Trial.Spec
// +kubebuilder:rbac:groups=trials.kubeflow.org,resources=trials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=trials.kubeflow.org,resources=trials/status,verbs=get;update;patch
func (r *ReconcileTrial) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	// Fetch the Trial instance
	logger := log.WithValues("Trial", request.NamespacedName)
	original := &trialsv1beta1.Trial{}
	err := r.Get(ctx, request.NamespacedName, original)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Trial Get error")
		return reconcile.Result{}, err
	}

	instance := original.DeepCopy()

	if needUpdate, finalizers := needUpdateFinalizers(instance); needUpdate {
		return r.updateFinalizers(instance, finalizers)
	}

	if !instance.IsCreated() {
		if instance.Status.StartTime == nil {
			now := metav1.Now()
			instance.Status.StartTime = &now
		}
		if instance.Status.CompletionTime == nil {
			instance.Status.CompletionTime = &metav1.Time{}
		}
		msg := "Trial is created"
		instance.MarkTrialStatusCreated(TrialCreatedReason, msg)
	} else {
		err := r.reconcileTrial(instance)
		if err != nil {
			if errors.Is(err, errMetricsNotReported) || errors.Is(err, errReportMetricsFailed) {
				return reconcile.Result{
					RequeueAfter: time.Second * 1,
				}, nil
			}
			// Requeue every 5s for real-time progress updates during training
			// But first save the status updates
			if errors.Is(err, errTrainerStatusPolling) {
				if !equality.Semantic.DeepEqual(original.Status, instance.Status) {
					if updateErr := r.updateStatusHandler(instance); updateErr != nil {
						logger.Info("Update trial instance status failed during polling, reconcile requeued", "err", updateErr)
						return reconcile.Result{Requeue: true}, nil
					}
				}
				return reconcile.Result{
					RequeueAfter: time.Second * 5,
				}, nil
			}
			logger.Error(err, "Reconcile trial error")
			r.recorder.Eventf(instance,
				corev1.EventTypeWarning, consts.ReconcileErrorReason,
				"Failed to reconcile: %v", err)
			return reconcile.Result{}, err
		}
	}

	if !equality.Semantic.DeepEqual(original.Status, instance.Status) {
		//assuming that only status change
		err = r.updateStatusHandler(instance)
		if err != nil {
			logger.Info("Update trial instance status failed, reconcile requeued", "err", err)
			return reconcile.Result{
				Requeue: true,
			}, nil
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileTrial) reconcileTrial(instance *trialsv1beta1.Trial) error {

	var err error
	logger := log.WithValues("Trial", types.NamespacedName{Name: instance.GetName(), Namespace: instance.GetNamespace()})
	desiredJob, err := r.getDesiredJobSpec(instance)
	if err != nil {
		logger.Error(err, "Job Spec Get error")
		return err
	}

	deployedJob, err := r.reconcileJob(instance, desiredJob)
	if err != nil {
		logger.Error(err, "Reconcile job error")
		return err
	}

	// Job already exists.
	// If Trial is EarlyStopped we need to verify/update observation logs.
	// In that case, Trial's job will be deleted even if metrics are not available.
	if deployedJob != nil && (!instance.IsCompleted() || instance.IsEarlyStopped()) {
		jobStatus, err := trialutil.GetDeployedJobStatus(instance, deployedJob)
		if err != nil {
			logger.Error(err, "GetDeployedJobStatus error")
		}

		// For TrainerStatusCollector: read TrainJob.status.trainerStatus directly (no sidecar needed)
		// Do this BEFORE checking jobStatus so we can update progress even when job status is unknown
		if instance.Spec.MetricsCollector.Collector.Kind == commonapiv1beta1.TrainerStatusCollector {
			// Pass existing observation to maintain min/max tracking across updates
			trainingProgress, observation := getTrainingProgressFromTrainJob(deployedJob, instance.Status.Observation)

			// Always update TrainingProgress (real-time progress)
			if trainingProgress != nil {
				instance.Status.TrainingProgress = trainingProgress
				logger.V(1).Info("Updated TrainingProgress from TrainJob",
					"progress", trainingProgress.ProgressPercentage,
					"metrics", len(trainingProgress.CurrentMetrics))
			}

			// Always update Observation to track min/max over time
			if observation != nil {
				instance.Status.Observation = observation
			}

			// Report intermediate metrics to DB for early stopping evaluation
			// This enables MedianStop to compare trials during training
			if observation != nil && len(observation.Metrics) > 0 {
				if err := r.reportObservationToDB(instance, observation); err != nil {
					logger.V(1).Info("Failed to report intermediate observation to DB", "err", err)
					// Non-fatal - continue with reconciliation
				}
			}

			// If job status not available yet, requeue to poll for updates
			if jobStatus == nil {
				return errTrainerStatusPolling
			}

			// Evaluate early stopping rules during training
			// For TrainerStatusCollector, we handle this in the controller since there's no sidecar
			logger.Info("Checking early stopping condition",
				"jobStatusNil", jobStatus == nil,
				"jobCondition", func() string { if jobStatus != nil { return string(jobStatus.Condition) }; return "nil" }(),
				"observationNil", observation == nil,
				"rulesCount", len(instance.Spec.EarlyStoppingRules))
			if jobStatus != nil && jobStatus.Condition == trialutil.JobRunning && observation != nil {
				if evaluateEarlyStoppingRules(instance, observation, trainingProgress) {
					logger.Info("Early stopping rule triggered, stopping trial")
					msg := "Trial is early stopped by early stopping rule"
					instance.MarkTrialStatusEarlyStopped(TrialEarlyStoppedReason, msg)

					// Delete the job to stop training
					if err := r.Delete(context.TODO(), deployedJob); err != nil {
						logger.Error(err, "Failed to delete job for early stopped trial")
						return err
					}
					r.recorder.Eventf(instance, corev1.EventTypeNormal, TrialEarlyStoppedReason, msg)
					return nil
				}
			}

			// Update Trial job status condition
			if err := r.UpdateTrialStatusCondition(instance, deployedJob.GetName(), jobStatus); err != nil {
				return err
			}

			// Requeue while job is running for real-time progress updates
			// TrainJob watch may not trigger for every trainerStatus change
			if jobStatus.Condition == trialutil.JobRunning {
				return errTrainerStatusPolling
			}
			return nil
		}

		// For other collector types, skip if jobStatus is nil
		if jobStatus == nil {
			return nil
		}

		// For other collector types: use DB-based observation (original behavior)
		if jobStatus.Condition == trialutil.JobSucceeded || instance.IsEarlyStopped() {
			if err = r.UpdateTrialStatusObservation(instance); err != nil {
				logger.Error(err, "Update trial status observation error")
				return err
			}
		}

		// If observation is empty, metrics collector doesn't finish.
		// We need to requeue reconcile when the Trial is succeeded, metrics collector's type is not `Push`, and metrics are not reported.
		if jobStatus.Condition == trialutil.JobSucceeded &&
			instance.Status.Observation == nil &&
			instance.Spec.MetricsCollector.Collector.Kind != commonapiv1beta1.PushCollector {
			logger.Info("Trial job is succeeded but metrics are not reported, reconcile requeued")
			return errMetricsNotReported
		}

		// Update Trial job status only
		//    if job has succeeded and if observation field is available.
		//    if job has failed
		// This will ensure that trial is set to be complete only if metric is collected at least once
		if err := r.UpdateTrialStatusCondition(instance, deployedJob.GetName(), jobStatus); err != nil {
			return err
		}

		return nil
	}
	return nil
}

func (r *ReconcileTrial) reconcileJob(instance *trialsv1beta1.Trial, desiredJob *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	var err error
	logger := log.WithValues("Trial", types.NamespacedName{Name: instance.GetName(), Namespace: instance.GetNamespace()})
	apiVersion := desiredJob.GetAPIVersion()
	kind := desiredJob.GetKind()
	gvk := schema.FromAPIVersionAndKind(apiVersion, kind)

	deployedJob := &unstructured.Unstructured{}
	deployedJob.SetGroupVersionKind(gvk)
	err = r.Get(context.TODO(), types.NamespacedName{Name: desiredJob.GetName(), Namespace: desiredJob.GetNamespace()}, deployedJob)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if instance.IsCompleted() {
				return nil, nil
			}

			logger.Info("Creating Job", "kind", kind,
				"name", desiredJob.GetName())
			err = r.Create(context.TODO(), desiredJob)
			if err != nil {
				logger.Error(err, "Create job error")
				return nil, err
			}
			// We should assign desiredJob to deployedJob to update Trial status
			// properly on the first reconcile run.
			deployedJob = desiredJob

			eventMsg := fmt.Sprintf("Job %s has been created", desiredJob.GetName())
			r.recorder.Eventf(instance, corev1.EventTypeNormal, JobCreatedReason, eventMsg)
		} else {
			logger.Error(err, "Trial Get error")
			return nil, err
		}
	} else {
		if instance.IsCompleted() && !instance.Spec.RetainRun {
			if err = r.Delete(context.TODO(), desiredJob, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
				logger.Error(err, "Delete job error")
				return nil, err
			} else {
				eventMsg := fmt.Sprintf("Job %s has been deleted", desiredJob.GetName())
				r.recorder.Eventf(instance, corev1.EventTypeNormal, JobDeletedReason, eventMsg)
				return nil, nil
			}
		}
	}

	return deployedJob, nil
}

func (r *ReconcileTrial) getDesiredJobSpec(instance *trialsv1beta1.Trial) (*unstructured.Unstructured, error) {

	logger := log.WithValues("Trial", types.NamespacedName{Name: instance.GetName(), Namespace: instance.GetNamespace()})

	desiredJobSpec := instance.Spec.RunSpec

	if err := controllerutil.SetControllerReference(instance, desiredJobSpec, r.scheme); err != nil {
		logger.Error(err, "Set controller reference error")
		return nil, err
	}

	return desiredJobSpec, nil
}
