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

interface TrialMetricData {
  trialName: string;
  progress: number;
  metricValue: number | null;
  status: string;
}

interface TrialHistoryPoint {
  progress: number;
  metricValue: number;
}

@Component({
  selector: 'app-convergence-chart',
  templateUrl: './convergence-chart.component.html',
  styleUrls: ['./convergence-chart.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ConvergenceChartComponent implements OnChanges {
  @Input() trialsProgress: TrialProgress[] = [];
  @Input() objectiveMetricName = 'loss';
  @Input() objectiveType: 'minimize' | 'maximize' = 'minimize';

  chartOptions: any = {};
  initOpts = { renderer: 'svg' };

  // Track historical progress for each trial to draw trajectory lines
  private trialHistory: Map<string, TrialHistoryPoint[]> = new Map();
  private trialStatuses: Map<string, string> = new Map();

  private colors = [
    '#1976d2',
    '#388e3c',
    '#f57c00',
    '#7b1fa2',
    '#c2185b',
    '#00796b',
    '#5d4037',
    '#455a64',
  ];

  // Color for pruned/early-stopped trials
  private prunedColor = '#d32f2f';

  ngOnChanges(changes: SimpleChanges): void {
    if (changes.trialsProgress && this.trialsProgress?.length > 0) {
      this.updateTrialHistory();
      this.updateChart();
    }
  }

  private updateTrialHistory(): void {
    this.trialsProgress.forEach(trial => {
      if (
        trial.currentObjectiveValue === undefined ||
        trial.currentObjectiveValue === null
      ) {
        return;
      }

      const metricValue = parseFloat(trial.currentObjectiveValue);
      if (isNaN(metricValue)) return;

      // Update status tracking
      this.trialStatuses.set(trial.trialName, trial.status);

      // Get or create history for this trial
      let history = this.trialHistory.get(trial.trialName);
      if (!history) {
        history = [];
        this.trialHistory.set(trial.trialName, history);
      }

      // Add point if it's new (different progress or metric value)
      const lastPoint = history[history.length - 1];
      if (
        !lastPoint ||
        lastPoint.progress !== trial.progressPercentage ||
        Math.abs(lastPoint.metricValue - metricValue) > 1e-10
      ) {
        history.push({
          progress: trial.progressPercentage,
          metricValue: metricValue,
        });
      }
    });
  }

  private updateChart(): void {
    if (this.trialHistory.size === 0) {
      this.chartOptions = {};
      return;
    }

    const series = this.createSeries();
    const legend = Array.from(this.trialHistory.keys());

    this.chartOptions = {
      tooltip: {
        trigger: 'axis',
        formatter: (params: any) => this.formatTooltip(params),
      },
      legend: {
        data: legend,
        bottom: 0,
        type: 'scroll',
      },
      grid: {
        left: '3%',
        right: '4%',
        bottom: '15%',
        top: '10%',
        containLabel: true,
      },
      xAxis: {
        type: 'value',
        name: 'Progress (%)',
        nameLocation: 'middle',
        nameGap: 30,
        min: 0,
        max: 100,
        splitLine: {
          lineStyle: { type: 'dashed' },
        },
      },
      yAxis: {
        type: 'value',
        name: this.objectiveMetricName,
        nameLocation: 'middle',
        nameGap: 50,
        splitLine: {
          lineStyle: { type: 'dashed' },
        },
      },
      series,
      toolbox: {
        feature: {
          saveAsImage: { title: 'Save' },
          restore: { title: 'Reset' },
        },
      },
    };
  }

  private createSeries(): any[] {
    const series: any[] = [];
    let colorIndex = 0;

    this.trialHistory.forEach((history, trialName) => {
      const status = this.trialStatuses.get(trialName) || 'Unknown';
      const isPruned =
        status === 'EarlyStopped' || status === 'Killed' || status === 'Failed';
      const isRunning = status === 'Running';
      const isSucceeded = status === 'Succeeded';

      // Determine color based on status
      const baseColor = isPruned
        ? this.prunedColor
        : this.colors[colorIndex % this.colors.length];

      // Convert history to chart data format
      const data = history.map(point => [point.progress, point.metricValue]);

      // Main line series with trajectory
      series.push({
        name: trialName,
        type: 'line',
        smooth: true,
        showSymbol: true,
        symbol: isPruned ? 'circle' : isRunning ? 'circle' : 'emptyCircle',
        symbolSize: isRunning ? 8 : 6,
        lineStyle: {
          width: isRunning ? 3 : 2,
          type: isPruned ? 'dashed' : 'solid',
          color: baseColor,
        },
        itemStyle: {
          color: baseColor,
          borderColor: isPruned ? this.prunedColor : baseColor,
          borderWidth: isPruned ? 2 : 1,
        },
        data: data,
        emphasis: {
          focus: 'series',
          lineStyle: { width: 4 },
        },
        // Mark the end point for pruned trials with an X
        markPoint: isPruned
          ? {
              symbol: 'circle',
              symbolSize: 16,
              data: [
                {
                  coord: data[data.length - 1],
                  itemStyle: {
                    color: this.prunedColor,
                    borderColor: '#fff',
                    borderWidth: 2,
                  },
                  label: {
                    show: true,
                    formatter: '✕',
                    fontSize: 10,
                    fontWeight: 'bold',
                    color: '#fff',
                  },
                },
              ],
            }
          : isRunning
            ? {
                symbol: 'pin',
                symbolSize: 30,
                data: [
                  {
                    coord: data[data.length - 1],
                    itemStyle: { color: baseColor },
                  },
                ],
              }
            : isSucceeded
              ? {
                  symbol: 'circle',
                  symbolSize: 12,
                  data: [
                    {
                      coord: data[data.length - 1],
                      itemStyle: {
                        color: '#4caf50',
                        borderColor: '#fff',
                        borderWidth: 2,
                      },
                      label: {
                        show: true,
                        formatter: '✓',
                        fontSize: 10,
                        fontWeight: 'bold',
                        color: '#fff',
                      },
                    },
                  ],
                }
              : undefined,
      });

      if (!isPruned) {
        colorIndex++;
      }
    });

    return series;
  }

  private formatTooltip(params: any): string {
    if (!params || params.length === 0) return '';

    let html = `Progress: ${params[0].data[0]}%<br/>`;
    params.forEach((param: any) => {
      const status = this.trialStatuses.get(param.seriesName) || '';
      const statusBadge =
        status === 'EarlyStopped' || status === 'Killed' || status === 'Failed'
          ? ' <span style="color:#d32f2f">[Pruned]</span>'
          : status === 'Running'
            ? ' <span style="color:#1976d2">[Running]</span>'
            : status === 'Succeeded'
              ? ' <span style="color:#4caf50">[Completed]</span>'
              : '';
      html += `${param.marker} ${param.seriesName}${statusBadge}: ${param.data[1].toFixed(6)}<br/>`;
    });
    return html;
  }
}
