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

interface TrialHistoryPoint {
  step: number;
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

  // Stats for display
  runningCount = 0;
  succeededCount = 0;
  prunedCount = 0;
  bestTrialName = '';
  bestTrialValue = '';

  // Track historical progress for each trial to draw trajectory lines
  private trialHistory: Map<string, TrialHistoryPoint[]> = new Map();
  private trialStatuses: Map<string, string> = new Map();
  private bestTrial = '';
  private storageKey = 'katib-convergence-history';

  private colors = [
    '#2196f3', // Blue
    '#4caf50', // Green
    '#ff9800', // Orange
    '#9c27b0', // Purple
    '#00bcd4', // Cyan
    '#e91e63', // Pink
    '#795548', // Brown
    '#607d8b', // Blue Grey
  ];

  private prunedColor = '#f44336';

  ngOnChanges(changes: SimpleChanges): void {
    if (changes.trialsProgress && this.trialsProgress?.length > 0) {
      this.loadHistoryFromStorage();
      this.updateTrialHistory();
      this.findBestTrial();
      this.updateChart();
      this.saveHistoryToStorage();
    }
  }

  private getStorageKey(): string {
    // Use experiment name from first trial to create unique key
    if (this.trialsProgress?.length > 0) {
      const parts = this.trialsProgress[0].trialName.split('-');
      parts.pop(); // Remove trial suffix
      return `${this.storageKey}-${parts.join('-')}`;
    }
    return this.storageKey;
  }

  private loadHistoryFromStorage(): void {
    try {
      const stored = sessionStorage.getItem(this.getStorageKey());
      if (stored) {
        const data = JSON.parse(stored);
        this.trialHistory = new Map(Object.entries(data.history || {}));
        this.trialStatuses = new Map(Object.entries(data.statuses || {}));
      }
    } catch (e) {
      console.warn('Failed to load convergence history:', e);
    }
  }

  private saveHistoryToStorage(): void {
    try {
      const historyObj: { [key: string]: TrialHistoryPoint[] } = {};
      const statusesObj: { [key: string]: string } = {};
      this.trialHistory.forEach((v, k) => (historyObj[k] = v));
      this.trialStatuses.forEach((v, k) => (statusesObj[k] = v));
      const data = { history: historyObj, statuses: statusesObj };
      sessionStorage.setItem(this.getStorageKey(), JSON.stringify(data));
    } catch (e) {
      console.warn('Failed to save convergence history:', e);
    }
  }

  private updateTrialHistory(): void {
    this.trialsProgress.forEach(trial => {
      this.trialStatuses.set(trial.trialName, trial.status);

      if (
        trial.currentObjectiveValue === undefined ||
        trial.currentObjectiveValue === null
      ) {
        return;
      }

      const metricValue = parseFloat(trial.currentObjectiveValue);
      if (isNaN(metricValue)) return;

      // Use currentStep if available, otherwise estimate from progress
      const step =
        trial.currentStep ??
        Math.round((trial.progressPercentage / 100) * (trial.totalSteps || 50));

      let history = this.trialHistory.get(trial.trialName);
      if (!history) {
        history = [];
        this.trialHistory.set(trial.trialName, history);
      }

      const lastPoint = history[history.length - 1];
      if (
        !lastPoint ||
        lastPoint.step !== step ||
        Math.abs(lastPoint.metricValue - metricValue) > 1e-10
      ) {
        history.push({
          step: step,
          metricValue: metricValue,
        });
      }
    });
  }

  private findBestTrial(): void {
    let bestValue: number | null = null;
    this.runningCount = 0;
    this.succeededCount = 0;
    this.prunedCount = 0;

    this.trialStatuses.forEach((status, trialName) => {
      if (status === 'Running') this.runningCount++;
      else if (status === 'Succeeded') this.succeededCount++;
      else if (
        status === 'EarlyStopped' ||
        status === 'Killed' ||
        status === 'Failed'
      )
        this.prunedCount++;
    });

    this.trialHistory.forEach((history, trialName) => {
      if (history.length > 0) {
        const lastValue = history[history.length - 1].metricValue;
        if (bestValue === null) {
          bestValue = lastValue;
          this.bestTrial = trialName;
        } else if (this.objectiveType === 'minimize' && lastValue < bestValue) {
          bestValue = lastValue;
          this.bestTrial = trialName;
        } else if (this.objectiveType === 'maximize' && lastValue > bestValue) {
          bestValue = lastValue;
          this.bestTrial = trialName;
        }
      }
    });

    this.bestTrialName = this.bestTrial;
    this.bestTrialValue = bestValue !== null ? bestValue.toExponential(3) : '--';
  }

  private updateChart(): void {
    if (this.trialHistory.size === 0) {
      this.chartOptions = {};
      return;
    }

    const series = this.createSeries();
    const legendData = this.createLegendData();

    this.chartOptions = {
      animation: true,
      animationDuration: 300,
      title: {
        text: 'Real-Time Training Convergence',
        left: 'center',
        top: 5,
        textStyle: { fontSize: 16, fontWeight: 'bold' },
      },
      tooltip: {
        trigger: 'axis',
        backgroundColor: 'rgba(50, 50, 50, 0.9)',
        borderColor: '#333',
        textStyle: { color: '#fff', fontSize: 12 },
        formatter: (params: any) => this.formatTooltip(params),
      },
      legend: {
        data: legendData,
        bottom: 0,
        type: 'scroll',
        textStyle: { fontSize: 11 },
      },
      grid: {
        left: '3%',
        right: '4%',
        bottom: '12%',
        top: '12%',
        containLabel: true,
      },
      xAxis: {
        type: 'value',
        name: 'Training Steps',
        nameLocation: 'middle',
        nameGap: 28,
        min: 0,
        axisLine: { lineStyle: { color: '#999' } },
        splitLine: { lineStyle: { type: 'dashed', color: '#e0e0e0' } },
      },
      yAxis: {
        type: 'value',
        name: this.objectiveMetricName,
        nameLocation: 'middle',
        nameGap: 45,
        axisLine: { lineStyle: { color: '#999' } },
        splitLine: { lineStyle: { type: 'dashed', color: '#e0e0e0' } },
      },
      series,
      toolbox: {
        right: 20,
        feature: {
          dataZoom: { title: { zoom: 'Zoom', back: 'Reset' } },
          saveAsImage: { title: 'Save' },
          restore: { title: 'Reset' },
        },
      },
      dataZoom: [
        { type: 'inside', xAxisIndex: 0, filterMode: 'none' },
        { type: 'inside', yAxisIndex: 0, filterMode: 'none' },
      ],
    };
  }

  private createLegendData(): any[] {
    return Array.from(this.trialHistory.keys()).map(trialName => {
      const status = this.trialStatuses.get(trialName) || 'Unknown';
      const isPruned =
        status === 'EarlyStopped' || status === 'Killed' || status === 'Failed';
      const isBest = trialName === this.bestTrial;

      return {
        name: trialName,
        icon: isPruned ? 'circle' : isBest ? 'diamond' : 'roundRect',
      };
    });
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
      const isBest = trialName === this.bestTrial;

      const baseColor = isPruned
        ? this.prunedColor
        : this.colors[colorIndex % this.colors.length];

      const data = history.map(point => [point.step, point.metricValue]);

      series.push({
        name: trialName,
        type: 'line',
        smooth: 0.3,
        showSymbol: true,
        symbol: isPruned ? 'circle' : isRunning ? 'circle' : 'emptyCircle',
        symbolSize: isBest ? 8 : isRunning ? 7 : 5,
        lineStyle: {
          width: isBest ? 3 : isRunning ? 2.5 : 2,
          type: isPruned ? 'dashed' : 'solid',
          color: baseColor,
        },
        itemStyle: {
          color: baseColor,
          borderColor: isPruned ? '#fff' : baseColor,
          borderWidth: isPruned ? 2 : 1,
        },
        data: data,
        z: isBest ? 10 : isPruned ? 1 : 5,
        emphasis: {
          focus: 'series',
          lineStyle: { width: 4 },
          itemStyle: { borderWidth: 3 },
        },
        markPoint: this.getMarkPoint(
          data,
          isPruned,
          isRunning,
          isSucceeded,
          isBest,
          baseColor,
        ),
      });

      if (!isPruned) {
        colorIndex++;
      }
    });

    return series;
  }

  private getMarkPoint(
    data: number[][],
    isPruned: boolean,
    isRunning: boolean,
    isSucceeded: boolean,
    isBest: boolean,
    baseColor: string,
  ): any {
    if (data.length === 0) return undefined;
    const lastPoint = data[data.length - 1];

    if (isPruned) {
      return {
        symbol: 'circle',
        symbolSize: 22,
        data: [
          {
            coord: lastPoint,
            itemStyle: {
              color: this.prunedColor,
              borderColor: '#fff',
              borderWidth: 2,
            },
            label: {
              show: true,
              formatter: '✕',
              fontSize: 12,
              fontWeight: 'bold',
              color: '#fff',
            },
          },
        ],
      };
    }

    if (isRunning) {
      return {
        symbol: 'pin',
        symbolSize: 35,
        data: [
          {
            coord: lastPoint,
            itemStyle: { color: baseColor },
            label: {
              show: true,
              formatter: (params: any) => {
                const val = params.data.coord[1];
                return val < 0.001 ? val.toExponential(1) : val.toFixed(3);
              },
              fontSize: 9,
              color: '#fff',
            },
          },
        ],
      };
    }

    if (isSucceeded) {
      return {
        symbol: 'circle',
        symbolSize: isBest ? 20 : 16,
        data: [
          {
            coord: lastPoint,
            itemStyle: {
              color: isBest ? '#ffc107' : '#4caf50',
              borderColor: '#fff',
              borderWidth: 2,
            },
            label: {
              show: true,
              formatter: isBest ? '★' : '✓',
              fontSize: isBest ? 12 : 10,
              fontWeight: 'bold',
              color: isBest ? '#000' : '#fff',
            },
          },
        ],
      };
    }

    return undefined;
  }

  private formatTooltip(params: any): string {
    if (!params || params.length === 0) return '';

    let html = `<b>Step: ${params[0].data[0]}</b><br/>`;

    const sortedParams = [...params].sort((a, b) => {
      const aVal = a.data[1];
      const bVal = b.data[1];
      return this.objectiveType === 'minimize' ? aVal - bVal : bVal - aVal;
    });

    sortedParams.forEach((param: any) => {
      const status = this.trialStatuses.get(param.seriesName) || '';
      const isBest = param.seriesName === this.bestTrial;
      const isPruned =
        status === 'EarlyStopped' || status === 'Killed' || status === 'Failed';

      let badge = '';
      if (isBest) badge = ' <span style="color:#ffc107">★ Best</span>';
      else if (isPruned)
        badge = ' <span style="color:#d32f2f">[Pruned]</span>';
      else if (status === 'Running')
        badge = ' <span style="color:#1976d2">[Running]</span>';
      else if (status === 'Succeeded')
        badge = ' <span style="color:#4caf50">[Done]</span>';

      html += `${param.marker} ${param.seriesName}${badge}: ${param.data[1].toFixed(6)}<br/>`;
    });

    return html;
  }
}
