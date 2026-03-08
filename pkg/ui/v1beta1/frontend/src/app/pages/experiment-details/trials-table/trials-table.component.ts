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

  private mergeProgressData(): void {
    if (!this.trialsProgress?.length) return;

    const progressMap = new Map<string, TrialProgress>();
    this.trialsProgress.forEach(p => progressMap.set(p.trialName, p));

    this.processedData.forEach(row => {
      const trialName = row['trial name'];
      const progress = progressMap.get(trialName);
      if (progress) {
        row['progress'] = progress.progressPercentage;
        row['eta'] = this.formatEta(
          (progress as any).estimatedRemainingSeconds,
        );
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
    for (var i = 0; i < displayedColumns.length; i++) {
      if (displayedColumns[i] !== 'Kfp run') {
        if (displayedColumns[i] === 'Trial name') {
          columns.push({
            matHeaderCellDef: displayedColumns[i],
            matColumnDef: 'name',
            style: { width: '20%' },
            value: new LinkValue({
              field: 'link',
              popoverField: 'trial name',
              truncate: true,
              linkType: LinkType.Internal,
            }),
            sort: true,
          });
        } else if (displayedColumns[i] === 'Status') {
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
        style: { width: '12%' },
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
        style: { width: '10%' },
        value: new PropertyValue({
          field: 'eta',
          valueFn: (row: any) => row.eta || '--',
        }),
        sort: true,
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
