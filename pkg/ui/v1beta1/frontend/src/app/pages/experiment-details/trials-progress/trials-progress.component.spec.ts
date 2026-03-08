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
import { TrialsProgressComponent } from './trials-progress.component';
import { TrialsProgressModule } from './trials-progress.module';

describe('TrialsProgressComponent', () => {
  let component: TrialsProgressComponent;
  let fixture: ComponentFixture<TrialsProgressComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TrialsProgressModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TrialsProgressComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should sort running trials first', () => {
    component.trialsProgress = [
      { trialName: 'trial-1', progressPercentage: 50, status: 'Succeeded' },
      { trialName: 'trial-2', progressPercentage: 30, status: 'Running' },
      { trialName: 'trial-3', progressPercentage: 80, status: 'Failed' },
    ];
    component.ngOnChanges({
      trialsProgress: {
        currentValue: component.trialsProgress,
        previousValue: [],
        firstChange: true,
        isFirstChange: () => true,
      },
    });

    expect(component.sortedTrials[0].trialName).toBe('trial-2');
    expect(component.sortedTrials[0].status).toBe('Running');
  });

  it('should return correct status icons', () => {
    expect(component.getStatusIcon('Running')).toBe('play_circle');
    expect(component.getStatusIcon('Succeeded')).toBe('check_circle');
    expect(component.getStatusIcon('Failed')).toBe('error');
    expect(component.getStatusIcon('EarlyStopped')).toBe('stop_circle');
  });

  it('should format ETA correctly', () => {
    expect(component.formatEta(30)).toBe('30s');
    expect(component.formatEta(90)).toBe('1m 30s');
    expect(component.formatEta(3700)).toBe('1h 1m');
    expect(component.formatEta(0)).toBe('--');
  });

  it('should return correct progress colors', () => {
    expect(component.getProgressColor(90)).toBe('primary');
    expect(component.getProgressColor(60)).toBe('accent');
    expect(component.getProgressColor(30)).toBe('warn');
  });
});
