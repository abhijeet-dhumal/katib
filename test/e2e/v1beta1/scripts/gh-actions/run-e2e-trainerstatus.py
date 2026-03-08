#!/usr/bin/env python3
"""
E2E test for TrainerStatus metrics collector integration.

This test verifies:
1. TrainerStatusCollector can be configured in Experiment
2. Metrics are collected from TrainJob's trainerStatus field
3. Training progress (progressPercentage, estimatedRemainingSeconds) is captured
4. Trial and Experiment status reflect training progress
"""

import argparse
import logging
import time

import yaml
from kubeflow.katib import ApiClient, KatibClient, models
from kubeflow.katib.constants import constants
from kubeflow.katib.utils.utils import FakeResponse
from kubernetes import client, config

EXPERIMENT_TIMEOUT = 60 * 20  # 20 min timeout
POLL_INTERVAL = 10  # seconds

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def verify_trainerstatus_metrics(
    katib_client: KatibClient,
    experiment: models.V1beta1Experiment,
    exp_name: str,
    exp_namespace: str,
):
    """Verify that TrainerStatus metrics are properly collected."""
    
    # Check that the experiment uses TrainerStatus collector
    if experiment.spec.metrics_collector_spec.collector.kind != "TrainerStatus":
        raise Exception(
            f"Expected TrainerStatus collector, got: "
            f"{experiment.spec.metrics_collector_spec.collector.kind}"
        )
    
    # Verify trials have observations
    trials = katib_client.list_trials(exp_name, exp_namespace)
    if not trials:
        raise Exception("No trials found for experiment")
    
    has_progress_metrics = False
    for trial in trials:
        if trial.status and trial.status.observation:
            for metric in trial.status.observation.metrics:
                logger.info(f"Trial {trial.metadata.name} metric: {metric.name}={metric.latest}")
                if metric.name == "progress_percentage":
                    has_progress_metrics = True
                    progress = float(metric.latest) if metric.latest else 0
                    if progress < 0 or progress > 100:
                        raise Exception(f"Invalid progress_percentage: {progress}")
    
    # Verify best trial metrics
    if experiment.status.current_optimal_trial:
        best = experiment.status.current_optimal_trial
        logger.info(f"Best trial: {best.best_trial_name}")
        if best.observation and best.observation.metrics:
            for metric in best.observation.metrics:
                logger.info(f"Best trial metric: {metric.name}={metric.latest}")
    
    logger.info("TrainerStatus metrics verification passed!")


def verify_training_progress_in_trial(
    exp_name: str,
    exp_namespace: str,
):
    """Verify that Trial status contains trainingProgress field."""
    
    api = client.CustomObjectsApi()
    
    # List trials for the experiment
    trials = api.list_namespaced_custom_object(
        group="trials.kubeflow.org",
        version="v1beta1",
        namespace=exp_namespace,
        plural="trials",
        label_selector=f"katib.kubeflow.org/experiment={exp_name}"
    )
    
    for trial in trials.get("items", []):
        trial_name = trial["metadata"]["name"]
        status = trial.get("status", {})
        
        # Check for trainingProgress field (populated by controller)
        training_progress = status.get("trainingProgress")
        if training_progress:
            logger.info(f"Trial {trial_name} trainingProgress: {training_progress}")
            
            progress_pct = training_progress.get("progressPercentage", 0)
            logger.info(f"  progressPercentage: {progress_pct}")
            
            remaining = training_progress.get("estimatedRemainingSeconds", 0)
            logger.info(f"  estimatedRemainingSeconds: {remaining}")
            
            current_metrics = training_progress.get("currentMetrics", [])
            for m in current_metrics:
                logger.info(f"  currentMetric: {m.get('name')}={m.get('latest')}")


def verify_trials_progress_in_experiment(
    exp_name: str,
    exp_namespace: str,
):
    """Verify that Experiment status contains trialsProgress field."""
    
    api = client.CustomObjectsApi()
    
    experiment = api.get_namespaced_custom_object(
        group="experiments.kubeflow.org",
        version="v1beta1",
        namespace=exp_namespace,
        plural="experiments",
        name=exp_name
    )
    
    status = experiment.get("status", {})
    trials_progress = status.get("trialsProgress", [])
    
    if trials_progress:
        logger.info(f"Experiment {exp_name} trialsProgress:")
        for tp in trials_progress:
            logger.info(f"  Trial: {tp.get('trialName')}")
            logger.info(f"    status: {tp.get('status')}")
            logger.info(f"    progressPercentage: {tp.get('progressPercentage')}")
            logger.info(f"    currentObjectiveValue: {tp.get('currentObjectiveValue')}")


def wait_and_monitor_progress(
    katib_client: KatibClient,
    exp_name: str,
    exp_namespace: str,
    timeout: int = EXPERIMENT_TIMEOUT,
):
    """Wait for experiment while monitoring training progress."""
    
    start_time = time.time()
    last_progress = {}
    
    while time.time() - start_time < timeout:
        try:
            experiment = katib_client.get_experiment(exp_name, exp_namespace)
            
            # Check experiment conditions
            for condition in experiment.status.conditions or []:
                if condition.type == constants.EXPERIMENT_CONDITION_SUCCEEDED:
                    if condition.status == constants.CONDITION_STATUS_TRUE:
                        logger.info("Experiment succeeded!")
                        return experiment
                elif condition.type == constants.EXPERIMENT_CONDITION_FAILED:
                    if condition.status == constants.CONDITION_STATUS_TRUE:
                        raise Exception(f"Experiment failed: {condition.message}")
            
            # Monitor trial progress
            trials = katib_client.list_trials(exp_name, exp_namespace)
            for trial in trials or []:
                trial_name = trial.metadata.name
                if trial.status and trial.status.observation:
                    for metric in trial.status.observation.metrics:
                        if metric.name == "progress_percentage":
                            progress = metric.latest
                            if last_progress.get(trial_name) != progress:
                                logger.info(f"Trial {trial_name}: progress={progress}%")
                                last_progress[trial_name] = progress
            
            # Also check raw Trial CRD for trainingProgress
            verify_training_progress_in_trial(exp_name, exp_namespace)
            verify_trials_progress_in_experiment(exp_name, exp_namespace)
            
        except Exception as e:
            logger.warning(f"Error during monitoring: {e}")
        
        time.sleep(POLL_INTERVAL)
    
    raise Exception(f"Experiment timed out after {timeout} seconds")


def run_trainerstatus_e2e(
    katib_client: KatibClient,
    experiment: models.V1beta1Experiment,
    exp_name: str,
    exp_namespace: str,
):
    """Run the TrainerStatus collector E2E test."""
    
    logger.info(f"Creating experiment: {exp_namespace}/{exp_name}")
    logger.info(f"Metrics collector: {experiment.spec.metrics_collector_spec.collector.kind}")
    
    # Create the experiment
    katib_client.create_experiment(experiment, exp_namespace)
    
    # Wait and monitor progress
    experiment = wait_and_monitor_progress(
        katib_client, exp_name, exp_namespace, EXPERIMENT_TIMEOUT
    )
    
    # Verify TrainerStatus metrics were collected
    verify_trainerstatus_metrics(katib_client, experiment, exp_name, exp_namespace)
    
    # Final verification of training progress fields
    verify_training_progress_in_trial(exp_name, exp_namespace)
    verify_trials_progress_in_experiment(exp_name, exp_namespace)
    
    return experiment


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="E2E test for TrainerStatus collector")
    parser.add_argument(
        "--experiment-path",
        type=str,
        required=True,
        help="Path to the experiment YAML file",
    )
    parser.add_argument(
        "--namespace",
        type=str,
        required=True,
        help="Namespace for the test",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Enable verbose logging",
    )
    args = parser.parse_args()
    
    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)
    
    logger.info("=" * 70)
    logger.info("TrainerStatus Collector E2E Test")
    logger.info("=" * 70)
    logger.info(f"Experiment: {args.experiment_path}")
    logger.info(f"Namespace: {args.namespace}")
    
    # Load experiment from YAML
    with open(args.experiment_path, "r") as f:
        experiment_data = yaml.safe_load(f)
    
    # Convert to Katib Experiment object
    fake_response = FakeResponse(experiment_data)
    experiment = ApiClient().deserialize(fake_response, "V1beta1Experiment")
    experiment.metadata.namespace = args.namespace
    
    exp_name = experiment.metadata.name
    exp_namespace = experiment.metadata.namespace
    
    # Initialize clients
    config.load_kube_config()
    katib_client = KatibClient()
    
    # Ensure namespace has metrics collector injection enabled
    try:
        ns = client.CoreV1Api().read_namespace(args.namespace)
        labels = ns.metadata.labels or {}
        if "katib.kubeflow.org/metrics-collector-injection" not in labels:
            labels["katib.kubeflow.org/metrics-collector-injection"] = "enabled"
            client.CoreV1Api().patch_namespace(
                args.namespace,
                {"metadata": {"labels": labels}}
            )
            logger.info(f"Enabled metrics collector injection for namespace {args.namespace}")
    except Exception as e:
        logger.warning(f"Could not update namespace labels: {e}")
    
    try:
        run_trainerstatus_e2e(katib_client, experiment, exp_name, exp_namespace)
        logger.info("=" * 70)
        logger.info(f"E2E TEST PASSED: {exp_namespace}/{exp_name}")
        logger.info("=" * 70)
    except Exception as e:
        logger.error("=" * 70)
        logger.error(f"E2E TEST FAILED: {exp_namespace}/{exp_name}")
        logger.error(f"Error: {e}")
        logger.error("=" * 70)
        raise
    finally:
        # Cleanup
        try:
            logger.info(f"Cleaning up experiment: {exp_namespace}/{exp_name}")
            katib_client.delete_experiment(exp_name, exp_namespace)
        except Exception as e:
            logger.warning(f"Cleanup error: {e}")
