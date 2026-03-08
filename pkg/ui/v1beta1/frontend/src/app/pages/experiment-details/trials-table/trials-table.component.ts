import {
  ChangeDetectionStrategy,
  Component,
  Input,
  OnChanges,
  SimpleChanges,
} from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import {
  PropertyValue,
  StatusValue,
  ComponentValue,
  TableConfig,
  LinkValue,
  LinkType,
} from 'kubeflow';
import { parseStatus } from '../../experiments/utils';
import lowerCase from 'lodash-es/lowerCase';
import { KfpRunComponent } from './kfp-run/kfp-run.component';
import { Router } from '@angular/router';
import { TrialProgress } from 'src/app/models/experiment.k8s.model';

@Component({
  selector: 'app-trials-table',
  templateUrl: './trials-table.component.html',
  styleUrls: ['./trials-table.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class TrialsTableComponent implements OnChanges {
  @Input()
  displayedColumns = [];

  @Input()
  data = [];

  @Input()
  experimentName = [];

  @Input()
  namespace: string;

  @Input()
  bestTrialName: string;

  @Input()
  trialsProgress: TrialProgress[] = [];

  bestTrialRow: {};

  config: TableConfig = { columns: [] };

  processedData = [];

  // Dynamic metric columns discovered from trialsProgress
  dynamicMetricColumns: string[] = [];

  constructor(public dialog: MatDialog, private router: Router) {}

  ngOnChanges(changes: SimpleChanges): void {
    if (
      (changes.displayedColumns && this.displayedColumns.length !== 0) ||
      changes.trialsProgress
    ) {
      this.displayedColumns = this.displayedColumns.slice(
        0,
        this.displayedColumns.length,
      );
      // Discover dynamic metric columns from trialsProgress
      this.discoverDynamicMetricColumns();
      this.processedData = this.setData(this.data, this.displayedColumns);
      this.mergeProgressData();
      this.config = this.setConfig(this.displayedColumns, this.processedData);
    }

    if (this.data.length > 0 && this.bestTrialName) {
      this.bestTrialRow = this.processedData.find(obj => {
        return obj['trial name'] === this.bestTrialName;
      });
    }
  }

  // Extract unique metric names from all trials' currentMetrics
  private discoverDynamicMetricColumns(): void {
    const metricSet = new Set<string>();

    this.trialsProgress?.forEach(progress => {
      progress.currentMetrics?.forEach(metric => {
        const colName = this.formatMetricColumnName(metric.name);
        metricSet.add(colName);
      });
    });

    // Filter out metrics that already exist in displayedColumns
    const existingLower = this.displayedColumns.map((c: string) =>
      c.toLowerCase(),
    );
    this.dynamicMetricColumns = Array.from(metricSet).filter(
      col => !existingLower.includes(col.toLowerCase()),
    );
  }

  // Format metric name for display (e.g., "grad_norm" -> "Grad Norm")
  private formatMetricColumnName(name: string): string {
    return name
      .replace(/_/g, ' ')
      .split(' ')
      .map(word => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
      .join(' ');
  }

  private mergeProgressData(): void {
    if (!this.trialsProgress?.length) return;

    const progressMap = new Map<string, TrialProgress>();
    this.trialsProgress.forEach(p => progressMap.set(p.trialName, p));

    this.processedData.forEach(row => {
      const trialName = row['trial name'];
      const progress = progressMap.get(trialName);
      if (progress) {
        row['progress'] = progress.progressPercentage;
        row['eta'] = this.formatEta(progress.estimatedRemainingSeconds);

        // Merge ALL real-time metrics from currentMetrics into row
        if (progress.currentMetrics?.length) {
          progress.currentMetrics.forEach(metric => {
            const colName = this.formatMetricColumnName(metric.name);
            const fieldKey = lowerCase(colName);
            row[fieldKey] = metric.latest;
          });
        }
      }
    });
  }

  private formatEta(seconds: number): string {
    if (!seconds || seconds <= 0) return '--';
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    const hours = Math.floor(seconds / 3600);
    const mins = Math.floor((seconds % 3600) / 60);
    return `${hours}h ${mins}m`;
  }

  setData(data: any, displayedColumns: any) {
    const processedData = [];
    for (var i = 0; i < data.length; i++) {
      var list = data[i];
      processedData[i] = {};

      for (var j = 0; j < displayedColumns.length; j++) {
        var key = lowerCase(displayedColumns[j]);
        var value = list[j];
        processedData[i][key] = value;

        if (key === 'trial name') {
          processedData[i].link = {
            text: list[j],
            url: `/experiment/${this.experimentName}/trial/${list[j]}`,
          };
        }
      }
    }

    return processedData;
  }

  setConfig(displayedColumns: any, processedData: any) {
    const columns = [];

    // Always add Trial Name as first column
    const trialNameIdx = displayedColumns.findIndex(
      (c: string) => c === 'Trial name',
    );
    if (trialNameIdx >= 0) {
      columns.push({
        matHeaderCellDef: 'Trial Name',
        matColumnDef: 'name',
        style: { width: '18%' },
        value: new LinkValue({
          field: 'link',
          popoverField: 'trial name',
          truncate: true,
          linkType: LinkType.Internal,
        }),
        sort: true,
      });
    }

    for (var i = 0; i < displayedColumns.length; i++) {
      if (
        displayedColumns[i] !== 'Kfp run' &&
        displayedColumns[i] !== 'Trial name'
      ) {
        if (displayedColumns[i] === 'Status') {
          columns.push({
            matHeaderCellDef: displayedColumns[i],
            matColumnDef: displayedColumns[i],
            value: new StatusValue({
              valueFn: parseStatus,
            }),
            sort: true,
          });
        } else {
          columns.push({
            matHeaderCellDef: displayedColumns[i],
            matColumnDef: displayedColumns[i],
            value: new PropertyValue({
              field: lowerCase(displayedColumns[i]),
            }),
            sort: true,
          });
        }
      }
    }

    // Add Progress column if trialsProgress data is available
    if (this.trialsProgress?.length > 0) {
      // Insert after Status column (index 1)
      const statusIndex = columns.findIndex(c => c.matColumnDef === 'Status');
      const insertIndex = statusIndex >= 0 ? statusIndex + 1 : 2;

      columns.splice(insertIndex, 0, {
        matHeaderCellDef: 'Progress',
        matColumnDef: 'Progress',
        style: { width: '8%' },
        value: new PropertyValue({
          field: 'progress',
          valueFn: (row: any) =>
            row.progress !== undefined ? `${row.progress}%` : '--',
        }),
        sort: true,
      });

      columns.splice(insertIndex + 1, 0, {
        matHeaderCellDef: 'ETA',
        matColumnDef: 'ETA',
        style: { width: '8%' },
        value: new PropertyValue({
          field: 'eta',
          valueFn: (row: any) => row.eta || '--',
        }),
        sort: true,
      });

      // Add dynamic metric columns from TrainerStatus (after ETA)
      let metricInsertIndex = insertIndex + 2;
      this.dynamicMetricColumns.forEach(colName => {
        const fieldKey = lowerCase(colName);
        columns.splice(metricInsertIndex, 0, {
          matHeaderCellDef: colName,
          matColumnDef: colName,
          value: new PropertyValue({
            field: fieldKey,
            valueFn: (row: any) => {
              const val = row[fieldKey];
              if (val === undefined || val === null || val === '') return '--';
              // Format numbers nicely
              const num = parseFloat(val);
              if (!isNaN(num)) {
                if (Math.abs(num) < 0.0001 || Math.abs(num) >= 10000) {
                  return num.toExponential(3);
                }
                return num.toFixed(4);
              }
              return val;
            },
          }),
          sort: true,
        });
        metricInsertIndex++;
      });
    }

    let kfpRunExists = false;
    for (var i = 0; i < processedData.length; i++) {
      if (processedData[i]['kfp run']) {
        kfpRunExists = true;
      }
    }

    if (kfpRunExists) {
      columns.push({
        matHeaderCellDef: '',
        matColumnDef: 'actions',
        value: new ComponentValue({
          component: KfpRunComponent,
        }),
      });
    }

    return {
      columns,
    };
  }
}
