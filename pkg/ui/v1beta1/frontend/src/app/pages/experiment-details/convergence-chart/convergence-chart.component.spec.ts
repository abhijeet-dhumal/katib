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

import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ConvergenceChartComponent } from './convergence-chart.component';
import { ConvergenceChartModule } from './convergence-chart.module';

describe('ConvergenceChartComponent', () => {
  let component: ConvergenceChartComponent;
  let fixture: ComponentFixture<ConvergenceChartComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ConvergenceChartModule],
    }).compileComponents();

    fixture = TestBed.createComponent(ConvergenceChartComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should identify best trial for minimize objective', () => {
    component.objectiveType = 'minimize';
    component.trialsProgress = [
      {
        trialName: 'trial-1',
        progressPercentage: 100,
        currentObjectiveValue: '0.5',
        status: 'Succeeded',
      },
      {
        trialName: 'trial-2',
        progressPercentage: 80,
        currentObjectiveValue: '0.2',
        status: 'Running',
      },
      {
        trialName: 'trial-3',
        progressPercentage: 90,
        currentObjectiveValue: '0.8',
        status: 'Succeeded',
      },
    ];
    component.ngOnChanges({
      trialsProgress: {
        currentValue: component.trialsProgress,
        previousValue: [],
        firstChange: true,
        isFirstChange: () => true,
      },
    });

    const best = component.getBestTrial();
    expect(best?.trialName).toBe('trial-2');
    expect(best?.metricValue).toBe(0.2);
  });

  it('should identify best trial for maximize objective', () => {
    component.objectiveType = 'maximize';
    component.trialsProgress = [
      {
        trialName: 'trial-1',
        progressPercentage: 100,
        currentObjectiveValue: '0.5',
        status: 'Succeeded',
      },
      {
        trialName: 'trial-2',
        progressPercentage: 80,
        currentObjectiveValue: '0.9',
        status: 'Running',
      },
    ];
    component.ngOnChanges({
      trialsProgress: {
        currentValue: component.trialsProgress,
        previousValue: [],
        firstChange: true,
        isFirstChange: () => true,
      },
    });

    const best = component.getBestTrial();
    expect(best?.trialName).toBe('trial-2');
    expect(best?.metricValue).toBe(0.9);
  });

  it('should update chart options when trials progress changes', () => {
    component.trialsProgress = [
      {
        trialName: 'trial-1',
        progressPercentage: 50,
        currentObjectiveValue: '0.3',
        status: 'Running',
      },
    ];
    component.ngOnChanges({
      trialsProgress: {
        currentValue: component.trialsProgress,
        previousValue: [],
        firstChange: true,
        isFirstChange: () => true,
      },
    });

    expect(component.chartOptions.series).toBeDefined();
    expect(component.chartOptions.series.length).toBe(1);
    expect(component.chartOptions.series[0].name).toBe('trial-1');
  });

  it('should handle empty trials progress', () => {
    component.trialsProgress = [];
    component.ngOnChanges({
      trialsProgress: {
        currentValue: component.trialsProgress,
        previousValue: [],
        firstChange: true,
        isFirstChange: () => true,
      },
    });

    expect(component.getBestTrial()).toBeNull();
  });
});
