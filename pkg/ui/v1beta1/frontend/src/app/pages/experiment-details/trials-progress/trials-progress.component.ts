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

import {
  Component,
  Input,
  OnChanges,
  SimpleChanges,
  ChangeDetectionStrategy,
} from '@angular/core';
import { TrialProgress } from 'src/app/models/experiment.k8s.model';

@Component({
  selector: 'app-trials-progress',
  templateUrl: './trials-progress.component.html',
  styleUrls: ['./trials-progress.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class TrialsProgressComponent implements OnChanges {
  @Input() trialsProgress: TrialProgress[] = [];
  @Input() objectiveType: 'minimize' | 'maximize' = 'minimize';

  sortedTrials: TrialProgress[] = [];

  ngOnChanges(changes: SimpleChanges): void {
    if (changes.trialsProgress && this.trialsProgress) {
      this.sortedTrials = this.sortTrials(this.trialsProgress);
    }
  }

  private sortTrials(trials: TrialProgress[]): TrialProgress[] {
    return [...trials].sort((a, b) => {
      if (a.status === 'Running' && b.status !== 'Running') return -1;
      if (a.status !== 'Running' && b.status === 'Running') return 1;

      if (a.currentObjectiveValue && b.currentObjectiveValue) {
        const aVal = parseFloat(a.currentObjectiveValue);
        const bVal = parseFloat(b.currentObjectiveValue);
        if (!isNaN(aVal) && !isNaN(bVal)) {
          return this.objectiveType === 'minimize' ? aVal - bVal : bVal - aVal;
        }
      }
      return b.progressPercentage - a.progressPercentage;
    });
  }

  getProgressColor(progress: number): string {
    if (progress >= 80) return 'primary';
    if (progress >= 50) return 'accent';
    return 'warn';
  }

  getStatusIcon(status: string): string {
    switch (status) {
      case 'Running':
        return 'play_circle';
      case 'Succeeded':
        return 'check_circle';
      case 'Failed':
        return 'error';
      case 'EarlyStopped':
        return 'stop_circle';
      case 'Killed':
        return 'cancel';
      default:
        return 'pending';
    }
  }

  getStatusClass(status: string): string {
    return `status-${status.toLowerCase()}`;
  }

  formatEta(seconds: number): string {
    if (!seconds || seconds <= 0) return '--';
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    const hours = Math.floor(seconds / 3600);
    const mins = Math.floor((seconds % 3600) / 60);
    return `${hours}h ${mins}m`;
  }

  trackByTrialName(index: number, trial: TrialProgress): string {
    return trial.trialName;
  }
}
