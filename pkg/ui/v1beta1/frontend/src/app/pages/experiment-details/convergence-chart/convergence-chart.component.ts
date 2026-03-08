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
  private bestTrial = '';

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

  private prunedColor = '#d32f2f';

  ngOnChanges(changes: SimpleChanges): void {
    if (changes.trialsProgress && this.trialsProgress?.length > 0) {
      this.updateTrialHistory();
      this.findBestTrial();
      this.updateChart();
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

      let history = this.trialHistory.get(trial.trialName);
      if (!history) {
        history = [];
        this.trialHistory.set(trial.trialName, history);
      }

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

  private findBestTrial(): void {
    let bestValue: number | null = null;
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
  }

  private updateChart(): void {
    if (this.trialHistory.size === 0) {
      this.chartOptions = {};
      return;
    }

    const series = this.createSeries();
    const legend = Array.from(this.trialHistory.keys());

    this.chartOptions = {
      animation: true,
      animationDuration: 300,
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
      dataZoom: [
        { type: 'inside', xAxisIndex: 0, filterMode: 'none' },
        { type: 'inside', yAxisIndex: 0, filterMode: 'none' },
      ],
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
      const isBest = trialName === this.bestTrial;

      const baseColor = isPruned
        ? this.prunedColor
        : this.colors[colorIndex % this.colors.length];

      const data = history.map(point => [point.progress, point.metricValue]);

      series.push({
        name: trialName,
        type: 'line',
        smooth: true,
        showSymbol: true,
        symbol: isPruned ? 'circle' : isRunning ? 'circle' : 'emptyCircle',
        symbolSize: isBest ? 6 : isRunning ? 5 : 4,
        lineStyle: {
          width: isBest ? 2 : isRunning ? 1.5 : 1,
          type: isPruned ? 'dashed' : 'solid',
          color: baseColor,
        },
        itemStyle: {
          color: baseColor,
          borderColor: isPruned ? this.prunedColor : baseColor,
          borderWidth: isPruned ? 2 : 1,
        },
        data: data,
        z: isBest ? 10 : isPruned ? 1 : 5,
        emphasis: {
          focus: 'series',
          lineStyle: { width: 2.5 },
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
        symbolSize: 14,
        data: [
          {
            coord: lastPoint,
            itemStyle: {
              color: this.prunedColor,
              borderColor: '#fff',
              borderWidth: 1,
            },
            label: {
              show: true,
              formatter: '✕',
              fontSize: 9,
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
        symbolSize: 25,
        data: [
          {
            coord: lastPoint,
            itemStyle: { color: baseColor },
            label: {
              show: true,
              formatter: (params: any) => params.data.coord[1].toFixed(4),
              fontSize: 8,
              color: '#fff',
            },
          },
        ],
      };
    }

    if (isSucceeded) {
      return {
        symbol: 'circle',
        symbolSize: isBest ? 12 : 10,
        data: [
          {
            coord: lastPoint,
            itemStyle: {
              color: isBest ? '#ffc107' : '#4caf50',
              borderColor: '#fff',
              borderWidth: 1,
            },
            label: {
              show: true,
              formatter: isBest ? '★' : '✓',
              fontSize: isBest ? 8 : 7,
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

    let html = `<b>Progress: ${params[0].data[0]}%</b><br/>`;

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
