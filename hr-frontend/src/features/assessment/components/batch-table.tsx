import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';
import type { TFunction } from 'i18next';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useBatches } from '@/features/assessment/hooks';
import type { BatchFilter } from '@/features/assessment/types';
import type { BatchListItem } from '@/lib/contracts';
import { cn } from '@/lib/utils';

export interface BatchTableProps {
  onCreateOpen: () => void;
  onFailuresOpen: (batchId: string) => void;
  onReSubmit: (batch: BatchListItem) => void;
}

const DEFAULT_PAGE_SIZE = 10;
const PAGE_SIZE_OPTIONS = [10, 20, 50];
const ALL = '__all__';

/** 评估对象摘要：all 显「全员」；specified ≤2 人直显，>2 人按「前两人 等 N 人」，title 悬浮全量名单（specs §4.1.5）。 */
function targetSummary(item: BatchListItem, t: TFunction): string {
  if (item.target_mode === 'all') return t('table.targetAll');
  if (item.target_names.length > 2) {
    return t('plan.targetSummary', {
      first: item.target_brief[0] ?? item.target_names[0],
      second: item.target_brief[1] ?? item.target_names[1],
      count: item.target_names.length,
    });
  }
  return item.target_names.join('、');
}

/** 状态标签变体：running 中性、success 系统、partial/failed 警示。 */
function statusVariant(status: BatchListItem['status']): 'default' | 'secondary' | 'destructive' {
  if (status === 'success') return 'secondary';
  if (status === 'partial_failed' || status === 'failed') return 'destructive';
  return 'default';
}

/** 批次列表：筛选 + 九列 + 行操作 + 分页。行操作可见性见 specs §4.1.3。 */
export function BatchTable({ onCreateOpen, onFailuresOpen, onReSubmit }: BatchTableProps): JSX.Element {
  const { t } = useTranslation('assessment');

  const [draftTrigger, setDraftTrigger] = useState<string>(ALL);
  const [draftStatus, setDraftStatus] = useState<string>(ALL);
  const [filter, setFilter] = useState<BatchFilter>({ page: 1, page_size: DEFAULT_PAGE_SIZE });

  const query = useBatches(filter);
  const list = query.data?.list ?? [];
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / filter.page_size));

  function onQuery() {
    setFilter({
      trigger_type: draftTrigger === ALL ? undefined : draftTrigger,
      status: draftStatus === ALL ? undefined : draftStatus,
      page: 1,
      page_size: filter.page_size,
    });
    if (query.isError) void query.refetch();
  }

  function onReset() {
    setDraftTrigger(ALL);
    setDraftStatus(ALL);
    setFilter({ page: 1, page_size: filter.page_size });
  }

  function onPageSizeChange(v: string) {
    setFilter({ page: 1, page_size: Number(v) });
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('table.title')}</CardTitle>
        <Button onClick={onCreateOpen}>{t('table.createAction')}</Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center gap-2">
          <Select value={draftTrigger} onValueChange={setDraftTrigger}>
            <SelectTrigger className="w-36" aria-label={t('table.filterTrigger')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('table.filterTriggerAll')}</SelectItem>
              <SelectItem value="scheduled">{t('table.triggerScheduled')}</SelectItem>
              <SelectItem value="manual">{t('table.triggerManual')}</SelectItem>
            </SelectContent>
          </Select>
          <Select value={draftStatus} onValueChange={setDraftStatus}>
            <SelectTrigger className="w-36" aria-label={t('table.filterStatus')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('table.filterStatusAll')}</SelectItem>
              <SelectItem value="running">{t('table.status.running')}</SelectItem>
              <SelectItem value="success">{t('table.status.success')}</SelectItem>
              <SelectItem value="partial_failed">{t('table.status.partial_failed')}</SelectItem>
              <SelectItem value="failed">{t('table.status.failed')}</SelectItem>
            </SelectContent>
          </Select>
          <Button variant="outline" onClick={onQuery}>
            {t('table.query')}
          </Button>
          <Button variant="ghost" onClick={onReset}>
            {t('table.reset')}
          </Button>
        </div>

        {query.isLoading ? (
          <div className="bg-muted h-40 w-full animate-pulse rounded-md" />
        ) : list.length === 0 ? (
          <p className="text-muted-foreground py-8 text-center text-sm">{t('table.empty')}</p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('table.colBatchNo')}</TableHead>
                  <TableHead>{t('table.colTriggerType')}</TableHead>
                  <TableHead>{t('table.colTarget')}</TableHead>
                  <TableHead>{t('table.colPeriod')}</TableHead>
                  <TableHead>{t('table.colStatus')}</TableHead>
                  <TableHead>{t('table.colProgress')}</TableHead>
                  <TableHead>{t('table.colSessions')}</TableHead>
                  <TableHead>{t('table.colFailed')}</TableHead>
                  <TableHead>{t('table.colTriggeredAt')}</TableHead>
                  <TableHead className="text-right">{t('table.colActions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="font-mono">{item.batch_no}</TableCell>
                    <TableCell>
                      <Badge variant={item.trigger_type === 'scheduled' ? 'secondary' : 'destructive'}>
                        {item.trigger_type === 'scheduled'
                          ? t('table.triggerScheduled')
                          : t('table.triggerManual')}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <span title={item.target_names.join('、')}>{targetSummary(item, t)}</span>
                    </TableCell>
                    <TableCell>
                      {item.period_start} ~ {item.period_end}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <Badge variant={statusVariant(item.status)}>
                          {t(`table.status.${item.status}`)}
                        </Badge>
                        {item.status === 'running' && item.stalled && (
                          <Badge variant="outline">{t('table.stalled')}</Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      {item.status === 'running' ? (
                        <div className="flex items-center gap-2">
                          <div
                            role="progressbar"
                            aria-valuenow={item.progress_percent}
                            aria-valuemin={0}
                            aria-valuemax={100}
                            className="bg-muted h-1.5 w-20 overflow-hidden rounded-full"
                          >
                            <div
                              className="bg-primary h-full"
                              style={{ width: `${item.progress_percent}%` }}
                            />
                          </div>
                          <span className="text-muted-foreground text-xs">
                            {item.evaluated_count}/{item.total_count} {item.progress_percent}%
                          </span>
                        </div>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                    <TableCell>{item.covered_session_count}</TableCell>
                    <TableCell>
                      <span className={cn(item.failed_count > 0 && 'text-destructive font-medium')}>
                        {item.failed_count}/{item.total_count}
                      </span>
                    </TableCell>
                    <TableCell>{item.triggered_at}</TableCell>
                    <TableCell className="text-right">
                      {item.failed_count > 0 && (
                        <Button variant="link" size="sm" onClick={() => onFailuresOpen(item.id)}>
                          {t('table.actionFailures')}
                        </Button>
                      )}
                      {item.stalled && item.failed_count === 0 && (
                        <Button variant="link" size="sm" onClick={() => onReSubmit(item)}>
                          {t('table.actionRestart')}
                        </Button>
                      )}
                      <Button variant="link" size="sm" disabled title={t('table.viewResultDisabled')}>
                        {t('table.actionViewResult')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            <div className="flex items-center justify-between">
              <span className="text-muted-foreground text-sm">
                {t('table.total', { total })}
              </span>
              <div className="flex items-center gap-2">
                <Select
                  value={String(filter.page_size)}
                  onValueChange={onPageSizeChange}
                >
                  <SelectTrigger size="sm" aria-label={t('table.pageSizeLabel')} title={t('table.pageSizeLabel')}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PAGE_SIZE_OPTIONS.map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {t(`table.pageSize${n}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={filter.page <= 1}
                  onClick={() => setFilter((f) => ({ ...f, page: f.page - 1 }))}
                >
                  {t('table.prevPage')}
                </Button>
                <span className="text-muted-foreground text-sm">
                  {filter.page} / {totalPages}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={filter.page >= totalPages}
                  onClick={() => setFilter((f) => ({ ...f, page: f.page + 1 }))}
                >
                  {t('table.nextPage')}
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
