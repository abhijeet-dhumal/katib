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

  ngOnChanges(changes: SimpleChanges): void {
    if (changes.trialsProgress && this.trialsProgress?.length > 0) {
      this.updateChart();
    }
  }

  private updateChart(): void {
    const trialData = this.prepareTrialData();

    if (trialData.length === 0) {
      this.chartOptions = {};
      return;
    }

    const series = this.createSeries(trialData);
    const legend = trialData.map(t => t.trialName);

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

  private prepareTrialData(): TrialMetricData[] {
    return this.trialsProgress
      .filter(trial => trial.currentObjectiveValue !== undefined)
      .map(trial => ({
        trialName: trial.trialName,
        progress: trial.progressPercentage,
        metricValue: trial.currentObjectiveValue
          ? parseFloat(trial.currentObjectiveValue)
          : null,
        status: trial.status,
      }))
      .filter(t => t.metricValue !== null && !isNaN(t.metricValue));
  }

  private createSeries(trialData: TrialMetricData[]): any[] {
    return trialData.map((trial, index) => ({
      name: trial.trialName,
      type: 'line',
      smooth: true,
      symbol: trial.status === 'Running' ? 'circle' : 'emptyCircle',
      symbolSize: trial.status === 'Running' ? 10 : 6,
      lineStyle: {
        width: trial.status === 'Running' ? 3 : 2,
        type: trial.status === 'EarlyStopped' ? 'dashed' : 'solid',
      },
      itemStyle: {
        color: this.colors[index % this.colors.length],
      },
      data: [[trial.progress, trial.metricValue]],
      emphasis: {
        focus: 'series',
        lineStyle: { width: 4 },
      },
      markPoint:
        trial.status === 'Running'
          ? {
              data: [
                {
                  coord: [trial.progress, trial.metricValue],
                  symbol: 'pin',
                  symbolSize: 30,
                  itemStyle: {
                    color: this.colors[index % this.colors.length],
                  },
                },
              ],
            }
          : undefined,
    }));
  }

  private formatTooltip(params: any): string {
    if (!params || params.length === 0) return '';

    let html = `<div style="font-weight: 600; margin-bottom: 8px;">Progress: ${params[0].data[0]}%</div>`;

    params.forEach((param: any) => {
      const trial = this.trialsProgress.find(
        t => t.trialName === param.seriesName,
      );
      const statusBadge = trial
        ? `<span style="
            font-size: 10px;
            padding: 1px 6px;
            border-radius: 8px;
            background: ${this.getStatusColor(trial.status)};
            color: white;
            margin-left: 8px;
          ">${trial.status}</span>`
        : '';

      html += `
        <div style="margin: 4px 0;">
          ${param.marker}
          <span style="font-weight: 500;">${param.seriesName}</span>
          ${statusBadge}
          <span style="float: right; font-family: monospace; margin-left: 16px;">
            ${param.data[1].toFixed(6)}
          </span>
        </div>
      `;
    });

    return html;
  }

  private getStatusColor(status: string): string {
    switch (status) {
      case 'Running':
        return '#1976d2';
      case 'Succeeded':
        return '#388e3c';
      case 'Failed':
        return '#d32f2f';
      case 'EarlyStopped':
        return '#f57c00';
      default:
        return '#757575';
    }
  }

  getBestTrial(): TrialMetricData | null {
    const validTrials = this.prepareTrialData();
    if (validTrials.length === 0) return null;

    return validTrials.reduce((best, current) => {
      if (!best) return current;
      if (this.objectiveType === 'minimize') {
        return current.metricValue! < best.metricValue! ? current : best;
      } else {
        return current.metricValue! > best.metricValue! ? current : best;
      }
    });
  }
}
