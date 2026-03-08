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

"""Unit tests for ProgressStop early stopping service."""

import unittest
from unittest.mock import MagicMock, patch

from pkg.apis.manager.v1beta1.python import api_pb2
from pkg.earlystopping.v1beta1.progressstop.service import (
    ProgressStopService,
    TrialProgress,
)


class TestTrialProgress(unittest.TestCase):
    """Tests for TrialProgress tracking class."""
    
    def test_update_progress(self):
        """Test metric history tracking."""
        tp = TrialProgress("test-trial")
        
        tp.update(0, 1.0)
        tp.update(10, 0.8)
        tp.update(20, 0.6)
        
        self.assertEqual(len(tp.metric_history), 3)
        self.assertEqual(tp.last_step, 20)
        self.assertEqual(tp.last_value, 0.6)
    
    def test_convergence_rate(self):
        """Test convergence rate calculation."""
        tp = TrialProgress("test-trial")
        
        tp.update(0, 1.0)
        tp.update(10, 0.8)
        tp.update(20, 0.6)
        
        # Improvement: 1.0 - 0.6 = 0.4 over 20 steps = 0.02 per step
        self.assertAlmostEqual(tp.convergence_rate, 0.02, places=4)
    
    def test_get_value_at_step_exact(self):
        """Test getting value at exact recorded step."""
        tp = TrialProgress("test-trial")
        
        tp.update(0, 1.0)
        tp.update(10, 0.5)
        tp.update(20, 0.3)
        
        self.assertEqual(tp.get_value_at_step(10), 0.5)
    
    def test_get_value_at_step_interpolated(self):
        """Test interpolation between steps."""
        tp = TrialProgress("test-trial")
        
        tp.update(0, 1.0)
        tp.update(10, 0.5)
        
        # At step 5, should interpolate to 0.75
        self.assertAlmostEqual(tp.get_value_at_step(5), 0.75, places=4)
    
    def test_get_value_at_step_empty(self):
        """Test getting value with no history."""
        tp = TrialProgress("test-trial")
        self.assertIsNone(tp.get_value_at_step(10))


class TestProgressStopService(unittest.TestCase):
    """Tests for ProgressStopService."""
    
    def setUp(self):
        """Set up test fixtures."""
        with patch('pkg.earlystopping.v1beta1.progressstop.service.config'):
            self.service = ProgressStopService()
    
    def test_validate_settings_valid(self):
        """Test validation of valid settings."""
        settings = [
            api_pb2.EarlyStoppingSetting(name="min_trials_required", value="2"),
            api_pb2.EarlyStoppingSetting(name="min_steps", value="10"),
            api_pb2.EarlyStoppingSetting(name="tolerance_factor", value="1.5"),
        ]
        
        is_valid, message = self.service._validate_settings(settings)
        self.assertTrue(is_valid)
        self.assertEqual(message, "")
    
    def test_validate_settings_invalid_min_trials(self):
        """Test validation rejects invalid min_trials_required."""
        settings = [
            api_pb2.EarlyStoppingSetting(name="min_trials_required", value="0"),
        ]
        
        is_valid, message = self.service._validate_settings(settings)
        self.assertFalse(is_valid)
        self.assertIn("min_trials_required", message)
    
    def test_validate_settings_invalid_tolerance(self):
        """Test validation rejects invalid tolerance_factor."""
        settings = [
            api_pb2.EarlyStoppingSetting(name="tolerance_factor", value="0.5"),
        ]
        
        is_valid, message = self.service._validate_settings(settings)
        self.assertFalse(is_valid)
        self.assertIn("tolerance_factor", message)
    
    def test_validate_settings_unknown(self):
        """Test validation rejects unknown settings."""
        settings = [
            api_pb2.EarlyStoppingSetting(name="unknown_setting", value="value"),
        ]
        
        is_valid, message = self.service._validate_settings(settings)
        self.assertFalse(is_valid)
        self.assertIn("Unknown", message)
    
    def test_parse_settings(self):
        """Test parsing of settings."""
        settings = [
            api_pb2.EarlyStoppingSetting(name="min_trials_required", value="3"),
            api_pb2.EarlyStoppingSetting(name="min_steps", value="20"),
            api_pb2.EarlyStoppingSetting(name="tolerance_factor", value="2.0"),
            api_pb2.EarlyStoppingSetting(name="use_convergence_rate", value="false"),
        ]
        
        self.service._parse_settings(settings)
        
        self.assertEqual(self.service.min_trials_required, 3)
        self.assertEqual(self.service.min_steps, 20)
        self.assertEqual(self.service.tolerance_factor, 2.0)
        self.assertFalse(self.service.use_convergence_rate)
    
    def test_generate_rules_not_enough_trials(self):
        """Test that no rules are generated with insufficient trials."""
        self.service.min_trials_required = 3
        self.service.min_steps = 10
        self.service.objective_type = api_pb2.MINIMIZE
        self.service.objective_metric = "loss"
        
        # Add only 2 trials
        tp1 = TrialProgress("trial-1")
        tp1.update(10, 0.5)
        tp2 = TrialProgress("trial-2")
        tp2.update(10, 0.6)
        
        self.service.trial_progress = {"trial-1": tp1, "trial-2": tp2}
        
        rules = self.service._generate_rules()
        self.assertEqual(len(rules), 0)
    
    def test_generate_rules_with_threshold(self):
        """Test threshold rule generation."""
        self.service.min_trials_required = 2
        self.service.min_steps = 5
        self.service.tolerance_factor = 1.5
        self.service.comparison_step_interval = 5
        self.service.objective_type = api_pb2.MINIMIZE
        self.service.objective_metric = "loss"
        self.service.comparison = api_pb2.GREATER
        
        # Add 2 trials with different performance
        tp1 = TrialProgress("trial-1")
        for i in range(6):
            tp1.update(i, 1.0 - i * 0.1)  # Good convergence: 1.0 -> 0.5
        
        tp2 = TrialProgress("trial-2")
        for i in range(6):
            tp2.update(i, 1.0 - i * 0.02)  # Poor convergence: 1.0 -> 0.9
        
        self.service.trial_progress = {"trial-1": tp1, "trial-2": tp2}
        
        rules = self.service._generate_rules()
        
        self.assertEqual(len(rules), 1)
        rule = rules[0]
        self.assertEqual(rule.name, "loss")
        # Best value is ~0.5, threshold should be 0.5 * 1.5 = 0.75
        self.assertAlmostEqual(float(rule.value), 0.75, places=1)
        self.assertEqual(rule.comparison, api_pb2.GREATER)


class TestProgressStopIntegration(unittest.TestCase):
    """Integration-style tests for ProgressStop service."""
    
    @patch('pkg.earlystopping.v1beta1.progressstop.service.config')
    @patch('pkg.earlystopping.v1beta1.progressstop.service.grpc.insecure_channel')
    def test_get_early_stopping_rules_flow(self, mock_channel, mock_config):
        """Test the full GetEarlyStoppingRules flow."""
        service = ProgressStopService()
        
        # Create mock experiment
        experiment = api_pb2.Experiment(
            name="test-experiment",
            spec=api_pb2.ExperimentSpec(
                objective=api_pb2.ObjectiveSpec(
                    type=api_pb2.MINIMIZE,
                    objective_metric_name="loss",
                ),
                early_stopping=api_pb2.EarlyStoppingSpec(
                    algorithm_name="progressstop",
                    algorithm_settings=[
                        api_pb2.EarlyStoppingSetting(
                            name="min_trials_required", value="2"
                        ),
                        api_pb2.EarlyStoppingSetting(name="min_steps", value="5"),
                    ],
                ),
            ),
        )
        
        # Create mock trials
        trials = [
            api_pb2.Trial(
                name="trial-1",
                status=api_pb2.TrialStatus(
                    condition=api_pb2.TrialStatus.TrialConditionType.RUNNING
                ),
            ),
            api_pb2.Trial(
                name="trial-2",
                status=api_pb2.TrialStatus(
                    condition=api_pb2.TrialStatus.TrialConditionType.RUNNING
                ),
            ),
        ]
        
        request = api_pb2.GetEarlyStoppingRulesRequest(
            experiment=experiment,
            trials=trials,
            db_manager_address="katib-db-manager:6789",
        )
        
        # Mock DB response
        mock_stub = MagicMock()
        mock_channel.return_value.__enter__.return_value = MagicMock()
        
        context = MagicMock()
        
        # First call initializes the service
        reply = service.GetEarlyStoppingRules(request, context)
        
        # Verify settings were parsed
        self.assertEqual(service.min_trials_required, 2)
        self.assertEqual(service.min_steps, 5)
        self.assertEqual(service.objective_metric, "loss")


if __name__ == "__main__":
    unittest.main()
