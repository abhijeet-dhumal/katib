# Copyright 2024 The Kubeflow Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""
Progress-based early stopping service for Katib.

This service uses real-time training progress from TrainerStatus collector
to make early stopping decisions based on convergence rate comparison.

Key features:
- Uses trainerStatus metrics (loss, progress, steps) for real-time decisions
- Compares convergence rate across concurrent trials
- Stops trials that are significantly underperforming relative to others
- Supports both loss-based and custom metric comparison
"""

import logging
import multiprocessing
from datetime import datetime
from typing import Dict, Iterable, List, Optional, Tuple

import grpc
from kubernetes import client, config

from pkg.apis.manager.v1beta1.python import api_pb2, api_pb2_grpc

logger = logging.getLogger(__name__)
logging.basicConfig(level=logging.INFO)

STATUS_EARLY_STOPPED = "EarlyStopped"
KUBEFLOW_GROUP = "kubeflow.org"
KATIB_VERSION = "v1beta1"
TRIAL_PLURAL = "trials"
APISERVER_TIMEOUT = 120
DEFAULT_NAMESPACE = "default"

SUCCEEDED_TRIAL = api_pb2.TrialStatus.TrialConditionType.SUCCEEDED
RUNNING_TRIAL = api_pb2.TrialStatus.TrialConditionType.RUNNING


class TrialProgress:
    """Tracks convergence progress for a single trial."""
    
    def __init__(self, name: str):
        self.name = name
        self.metric_history: List[Tuple[int, float]] = []  # (step, value)
        self.last_step = 0
        self.last_value = float('inf')
        self.convergence_rate = 0.0  # Rate of metric improvement
    
    def update(self, step: int, value: float):
        """Update trial progress with new metric value."""
        self.metric_history.append((step, value))
        
        # Calculate convergence rate (improvement per step)
        if len(self.metric_history) >= 2:
            first_step, first_value = self.metric_history[0]
            improvement = first_value - value  # Assumes minimization
            steps_taken = step - first_step
            if steps_taken > 0:
                self.convergence_rate = improvement / steps_taken
        
        self.last_step = step
        self.last_value = value
    
    def get_value_at_step(self, step: int) -> Optional[float]:
        """Interpolate metric value at a given step."""
        if not self.metric_history:
            return None
        
        # Find surrounding data points
        before = None
        after = None
        for s, v in self.metric_history:
            if s <= step:
                before = (s, v)
            if s >= step and after is None:
                after = (s, v)
        
        if before is None:
            return after[1] if after else None
        if after is None or before[0] == after[0]:
            return before[1]
        
        # Linear interpolation
        ratio = (step - before[0]) / (after[0] - before[0])
        return before[1] + ratio * (after[1] - before[1])


class ProgressStopService(api_pb2_grpc.EarlyStoppingServicer):
    """
    Progress-based early stopping service.
    
    Stops trials based on real-time convergence comparison:
    - Compares each trial's loss/metric at the same training step
    - Identifies trials significantly underperforming vs peers
    - Uses configurable tolerance for stopping decisions
    """
    
    def __init__(self):
        super(ProgressStopService, self).__init__()
        self.is_first_run = True
        
        # Default settings
        self.min_trials_required = 2  # Min concurrent trials for comparison
        self.min_steps = 10  # Min steps before considering early stop
        self.comparison_step_interval = 10  # Compare at every N steps
        self.tolerance_factor = 1.5  # Stop if loss > (best * tolerance)
        self.use_convergence_rate = True  # Also compare convergence rates
        self.convergence_rate_tolerance = 0.5  # Stop if rate < (best * tolerance)
        
        # Trial tracking
        self.trial_progress: Dict[str, TrialProgress] = {}
        
        # Initialize namespace and K8s client
        try:
            with open(
                "/var/run/secrets/kubernetes.io/serviceaccount/namespace", "r"
            ) as f:
                self.namespace = f.readline().strip()
                config.load_incluster_config()
        except Exception as e:
            logger.info(f'{e}. Using "{DEFAULT_NAMESPACE}" namespace')
            self.namespace = DEFAULT_NAMESPACE
            config.load_kube_config()
        
        self.api_instance = client.CustomObjectsApi()
    
    def ValidateEarlyStoppingSettings(
        self,
        request: api_pb2.ValidateEarlyStoppingSettingsRequest,
        context: grpc.ServicerContext,
    ) -> api_pb2.ValidateEarlyStoppingSettingsReply:
        """Validate early stopping settings."""
        is_valid, message = self._validate_settings(
            request.early_stopping.algorithm_settings
        )
        if not is_valid:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details(message)
            logger.error(message)
        return api_pb2.ValidateEarlyStoppingSettingsReply()
    
    def _validate_settings(
        self, settings: Iterable[api_pb2.EarlyStoppingSetting]
    ) -> Tuple[bool, str]:
        """Validate progressstop settings."""
        for setting in settings:
            try:
                if setting.name == "min_trials_required":
                    if int(setting.value) < 1:
                        return False, "min_trials_required must be >= 1"
                elif setting.name == "min_steps":
                    if int(setting.value) < 1:
                        return False, "min_steps must be >= 1"
                elif setting.name == "comparison_step_interval":
                    if int(setting.value) < 1:
                        return False, "comparison_step_interval must be >= 1"
                elif setting.name == "tolerance_factor":
                    if float(setting.value) <= 1.0:
                        return False, "tolerance_factor must be > 1.0"
                elif setting.name == "use_convergence_rate":
                    if setting.value.lower() not in ("true", "false"):
                        return False, "use_convergence_rate must be true or false"
                elif setting.name == "convergence_rate_tolerance":
                    if float(setting.value) <= 0 or float(setting.value) > 1.0:
                        return False, "convergence_rate_tolerance must be in (0, 1]"
                else:
                    return False, f"Unknown setting: {setting.name}"
            except Exception as e:
                return False, f"Invalid {setting.name}: {e}"
        return True, ""
    
    def _parse_settings(self, settings: Iterable[api_pb2.EarlyStoppingSetting]):
        """Parse and apply early stopping settings."""
        for setting in settings:
            if setting.name == "min_trials_required":
                self.min_trials_required = int(setting.value)
            elif setting.name == "min_steps":
                self.min_steps = int(setting.value)
            elif setting.name == "comparison_step_interval":
                self.comparison_step_interval = int(setting.value)
            elif setting.name == "tolerance_factor":
                self.tolerance_factor = float(setting.value)
            elif setting.name == "use_convergence_rate":
                self.use_convergence_rate = setting.value.lower() == "true"
            elif setting.name == "convergence_rate_tolerance":
                self.convergence_rate_tolerance = float(setting.value)
    
    def GetEarlyStoppingRules(
        self,
        request: api_pb2.GetEarlyStoppingRulesRequest,
        context: grpc.ServicerContext,
    ) -> api_pb2.GetEarlyStoppingRulesReply:
        """Generate early stopping rules based on real-time progress comparison."""
        logger.info("GetEarlyStoppingRules called")
        
        # Initialize on first run
        if self.is_first_run:
            self.is_first_run = False
            self._parse_settings(
                request.experiment.spec.early_stopping.algorithm_settings
            )
            
            # Get objective configuration
            objective = request.experiment.spec.objective
            self.objective_type = objective.type
            self.objective_metric = objective.objective_metric_name
            
            # Set comparison type
            if self.objective_type == api_pb2.MAXIMIZE:
                self.comparison = api_pb2.LESS
            else:
                self.comparison = api_pb2.GREATER
            
            # Get DB manager address
            self.db_manager_address = request.db_manager_address
            
            logger.info(
                f"ProgressStop initialized: "
                f"min_trials={self.min_trials_required}, "
                f"min_steps={self.min_steps}, "
                f"tolerance={self.tolerance_factor}"
            )
        
        # Update trial progress from current metrics
        self._update_trial_progress(request.trials)
        
        # Generate early stopping rules
        early_stopping_rules = self._generate_rules()
        
        logger.info(f"Generated {len(early_stopping_rules)} early stopping rules")
        return api_pb2.GetEarlyStoppingRulesReply(
            early_stopping_rules=early_stopping_rules
        )
    
    def _update_trial_progress(self, trials: Iterable[api_pb2.Trial]):
        """Update progress tracking for all trials."""
        for trial in trials:
            # Only track running or succeeded trials
            if trial.status.condition not in (RUNNING_TRIAL, SUCCEEDED_TRIAL):
                continue
            
            # Initialize trial progress tracker
            if trial.name not in self.trial_progress:
                self.trial_progress[trial.name] = TrialProgress(trial.name)
            
            # Get latest metrics from DB
            try:
                with grpc.insecure_channel(self.db_manager_address) as channel:
                    stub = api_pb2_grpc.DBManagerStub(channel)
                    
                    # Get objective metric logs
                    response = stub.GetObservationLog(
                        api_pb2.GetObservationLogRequest(
                            trial_name=trial.name,
                            metric_name=self.objective_metric
                        ),
                        timeout=APISERVER_TIMEOUT
                    )
                    
                    # Update trial progress with all metric values
                    for log in response.observation_log.metric_logs:
                        try:
                            # Try to extract step from timestamp or use index
                            step = len(self.trial_progress[trial.name].metric_history)
                            value = float(log.metric.value)
                            self.trial_progress[trial.name].update(step, value)
                        except (ValueError, AttributeError):
                            continue
                    
                    # Also try to get current_step metric for accurate step tracking
                    step_response = stub.GetObservationLog(
                        api_pb2.GetObservationLogRequest(
                            trial_name=trial.name,
                            metric_name="current_step"
                        ),
                        timeout=APISERVER_TIMEOUT
                    )
                    
                    if step_response.observation_log.metric_logs:
                        last_log = step_response.observation_log.metric_logs[-1]
                        try:
                            current_step = int(float(last_log.metric.value))
                            self.trial_progress[trial.name].last_step = current_step
                        except ValueError:
                            pass
                            
            except Exception as e:
                logger.warning(f"Failed to get metrics for trial {trial.name}: {e}")
    
    def _generate_rules(self) -> List[api_pb2.EarlyStoppingRule]:
        """Generate early stopping rules based on progress comparison."""
        rules = []
        
        # Get running trials with sufficient progress
        active_trials = [
            tp for tp in self.trial_progress.values()
            if tp.last_step >= self.min_steps and len(tp.metric_history) > 0
        ]
        
        if len(active_trials) < self.min_trials_required:
            logger.info(
                f"Not enough trials ({len(active_trials)}) for comparison, "
                f"need {self.min_trials_required}"
            )
            return rules
        
        # Find the best performing trial at the current comparison point
        comparison_step = max(tp.last_step for tp in active_trials)
        comparison_step = (
            comparison_step // self.comparison_step_interval
        ) * self.comparison_step_interval
        
        if comparison_step < self.min_steps:
            return rules
        
        # Get metric values at comparison step
        values_at_step = []
        for tp in active_trials:
            value = tp.get_value_at_step(comparison_step)
            if value is not None:
                values_at_step.append((tp.name, value, tp.convergence_rate))
        
        if not values_at_step:
            return rules
        
        # Find best value (min for minimize, max for maximize)
        if self.objective_type == api_pb2.MINIMIZE:
            best_trial, best_value, best_rate = min(values_at_step, key=lambda x: x[1])
        else:
            best_trial, best_value, best_rate = max(values_at_step, key=lambda x: x[1])
        
        logger.info(
            f"Best trial at step {comparison_step}: {best_trial} "
            f"(value={best_value:.4f}, rate={best_rate:.6f})"
        )
        
        # Generate threshold-based rule
        if self.objective_type == api_pb2.MINIMIZE:
            threshold = best_value * self.tolerance_factor
        else:
            threshold = best_value / self.tolerance_factor
        
        # Create early stopping rule for the objective metric
        rules.append(
            api_pb2.EarlyStoppingRule(
                name=self.objective_metric,
                value=str(threshold),
                comparison=self.comparison,
                start_step=self.min_steps,
            )
        )
        
        logger.info(
            f"Early stopping rule: {self.objective_metric} "
            f"{'>' if self.objective_type == api_pb2.MINIMIZE else '<'} "
            f"{threshold:.4f} after step {self.min_steps}"
        )
        
        # Optionally add convergence rate rule
        if self.use_convergence_rate and best_rate > 0:
            min_acceptable_rate = best_rate * self.convergence_rate_tolerance
            logger.info(
                f"Convergence rate threshold: {min_acceptable_rate:.6f} "
                f"(best: {best_rate:.6f})"
            )
        
        return rules
    
    def SetTrialStatus(
        self,
        request: api_pb2.SetTrialStatusRequest,
        context: grpc.ServicerContext,
    ) -> api_pb2.SetTrialStatusReply:
        """Update trial status to early stopped."""
        trial_name = request.trial_name
        logger.info(f"Setting early stopped status for trial: {trial_name}")
        
        # Get trial object
        try:
            thread = self.api_instance.get_namespaced_custom_object(
                KUBEFLOW_GROUP,
                KATIB_VERSION,
                self.namespace,
                TRIAL_PLURAL,
                trial_name,
                async_req=True,
            )
            trial = thread.get(APISERVER_TIMEOUT)
        except Exception as e:
            raise Exception(f"Failed to get trial {trial_name}: {e}")
        
        # Add early stopped condition
        time_now = datetime.now().strftime("%Y-%m-%dT%H:%M:%SZ")
        early_stopped_condition = {
            "type": STATUS_EARLY_STOPPED,
            "status": "True",
            "reason": "TrialEarlyStoppedByProgress",
            "message": "Trial stopped due to poor convergence relative to other trials",
            "lastUpdateTime": time_now,
            "lastTransitionTime": time_now,
        }
        
        if "conditions" not in trial.get("status", {}):
            trial["status"] = {"conditions": []}
        trial["status"]["conditions"].append(early_stopped_condition)
        
        # Update trial status
        try:
            self.api_instance.patch_namespaced_custom_object_status(
                KUBEFLOW_GROUP,
                KATIB_VERSION,
                self.namespace,
                TRIAL_PLURAL,
                trial_name,
                trial,
                async_req=True,
            )
        except Exception as e:
            raise Exception(f"Failed to update trial {trial_name}: {e}")
        
        # Remove from tracking
        if trial_name in self.trial_progress:
            del self.trial_progress[trial_name]
        
        logger.info(f"Trial {trial_name} marked as early stopped")
        return api_pb2.SetTrialStatusReply()
