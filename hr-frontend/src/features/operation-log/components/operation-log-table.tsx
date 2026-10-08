// 操作日志列表表格（specs P4_LOG_001 §4.1）：筛选区（操作人/类型/结果/时间范围）+ 工具栏
// （导出）+ 六列表格 + 分页。筛选与分页状态组件内部自治（profile-table 样板）。
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Loader2 } from 'lucide-react';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
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
import { usePageClamp } from '@/lib/use-table-state';

import { useOperationLogExport, useOperationLogList } from '../hooks';
import { moduleI18nKeys } from '../module-meta';
import { DetailDialog, ModuleBadge } from './detail-dialog';
import type { OperationLogFilter, OperationLogItem } from '../types';

/** 默认 10 条/页，可选 10/20/30（specs §4.1.4 规则5）。 */
const DEFAULT_PAGE_SIZE = 10;
const PAGE_SIZE_OPTIONS = [10, 20, 30];
const ALL = '__all__';

/** 八类操作类型筛选下拉顺序（specs §4.1.4 规则1 枚举序）。 */
const MODULE_OPTIONS = Object.keys(moduleI18nKeys);

/** 操作日志列表表格：上部筛选、中部六列表格、底部分页，行内详情开 DetailDialog。 */
export function OperationLogTable(): JSX.Element {
  const { t } = useTranslation('operationLog');

  // 操作人与时间范围是草稿态（回车/查询才提交，specs §4.1.3）；类型与结果即时生效
  const [draftOperator, setDraftOperator] = useState('');
  const [draftStart, setDraftStart] = useState('');
  const [draftEnd, setDraftEnd] = useState('');
  const [filter, setFilter] = useState<OperationLogFilter>({
    page: 1,
    page_size: DEFAULT_PAGE_SIZE,
  });
  const [selectedItem, setSelectedItem] = useState<OperationLogItem | null>(null);

  const query = useOperationLogList(filter);
  const exportMut = useOperationLogExport();

  const list = query.data?.list ?? [];
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / filter.page_size));
  // 页码钳位：total 收缩让当前页落空时回落末页（profile-table 先例）
  usePageClamp(filter.page, totalPages, (page) => setFilter((f) => ({ ...f, page })));

  const hasFilter = !!(
    filter.operator ||
    filter.module ||
    filter.result ||
    filter.start_date ||
    filter.end_date
  );

  function onQuery() {
    const start = draftStart || undefined;
    const end = draftEnd || undefined;
    // 起止顺序本地校验（specs §4.1.2 A 时间范围）：非法即提示拦截，不发请求
    if (start && end && start > end) {
      toast.error(t('validation.range'));
      return;
    }
    setFilter((f) => ({
      ...f,
      operator: draftOperator.trim() === '' ? undefined : draftOperator.trim(),
      start_date: start,
      end_date: end,
      page: 1,
    }));
    if (query.isError) void query.refetch();
  }

  function onReset() {
    setDraftOperator('');
    setDraftStart('');
    setDraftEnd('');
    setFilter({ page: 1, page_size: filter.page_size });
  }

  /** 即时筛选（specs §4.1.3）：下拉变更直接落 filter 并回第 1 页。 */
  function applyImmediate(patch: Partial<OperationLogFilter>) {
    setFilter((f) => ({ ...f, ...patch, page: 1 }));
  }

  function onExport() {
    // 按当前筛选导出（specs §4.1.3）：与列表已生效 filter 一致，不含未提交草稿
    exportMut.mutate(
      {
        operator: filter.operator,
        module: filter.module,
        result: filter.result,
        start_date: filter.start_date,
        end_date: filter.end_date,
      },
      {
        onError: () => toast.error(t('list.exportFailed')),
      },
    );
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('list.title')}</CardTitle>
        {/* 列表错误态导出禁用（specs §4.1.3）；pending 显 loading */}
        <Button onClick={onExport} disabled={query.isError || exportMut.isPending}>
          {exportMut.isPending && <Loader2 className="size-4 animate-spin" aria-hidden />}
          {exportMut.isPending ? t('list.exporting') : t('list.export')}
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            value={draftOperator}
            onChange={(e) => setDraftOperator(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onQuery();
            }}
            placeholder={t('list.operatorPlaceholder')}
            aria-label={t('list.operatorLabel')}
            className="w-44"
          />
          <Select
            value={filter.module ?? ALL}
            onValueChange={(v) => applyImmediate({ module: v === ALL ? undefined : v })}
          >
            <SelectTrigger className="w-36" aria-label={t('list.filterModule')}>
              <SelectValue placeholder={t('list.filterModuleAll')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('list.filterModuleAll')}</SelectItem>
              {MODULE_OPTIONS.map((m) => (
                <SelectItem key={m} value={m}>
                  {t(moduleI18nKeys[m])}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={filter.result ?? ALL}
            onValueChange={(v) => applyImmediate({ result: v === ALL ? undefined : v })}
          >
            <SelectTrigger className="w-32" aria-label={t('list.filterResult')}>
              <SelectValue placeholder={t('list.filterResultAll')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('list.filterResultAll')}</SelectItem>
              <SelectItem value="success">{t('list.resultSuccess')}</SelectItem>
              <SelectItem value="fail">{t('list.resultFail')}</SelectItem>
            </SelectContent>
          </Select>
          <Input
            type="date"
            value={draftStart}
            onChange={(e) => setDraftStart(e.target.value)}
            aria-label={t('list.startDate')}
            className="w-40"
          />
          <Input
            type="date"
            value={draftEnd}
            onChange={(e) => setDraftEnd(e.target.value)}
            aria-label={t('list.endDate')}
            className="w-40"
          />
          <Button variant="outline" onClick={onQuery}>
            {t('list.query')}
          </Button>
          <Button variant="ghost" onClick={onReset}>
            {t('list.reset')}
          </Button>
        </div>

        {query.isLoading ? (
          <div className="bg-muted h-40 w-full animate-pulse rounded-md" />
        ) : query.isError ? (
          <div className="flex flex-col items-center gap-2 py-8">
            <span className="text-muted-foreground text-sm">{t('list.loadError')}</span>
            <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
              {t('list.retry')}
            </Button>
          </div>
        ) : list.length === 0 ? (
          <p className="text-muted-foreground py-8 text-center text-sm">
            {hasFilter ? t('list.emptyFiltered') : t('list.empty')}
          </p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('list.colTime')}</TableHead>
                  <TableHead>{t('list.colOperator')}</TableHead>
                  <TableHead>{t('list.colModule')}</TableHead>
                  <TableHead>{t('list.colTarget')}</TableHead>
                  <TableHead>{t('list.colSummary')}</TableHead>
                  <TableHead>{t('list.colResult')}</TableHead>
                  <TableHead className="text-right">{t('list.colActions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="whitespace-nowrap">{item.created_at}</TableCell>
                    <TableCell className="font-medium">{item.operator}</TableCell>
                    <TableCell>
                      <ModuleBadge module={item.module} />
                    </TableCell>
                    <TableCell>
                      <span className="block max-w-56 truncate" title={item.target}>
                        {item.target}
                      </span>
                    </TableCell>
                    <TableCell>
                      {/* 摘要过长截断悬浮全文（specs §4.1.2 B，通用规范 11 条） */}
                      <span className="block max-w-64 truncate" title={item.summary}>
                        {item.summary}
                      </span>
                    </TableCell>
                    <TableCell>
                      {item.result === 'success' ? (
                        <Badge variant="secondary" className="text-success">
                          {t('list.resultSuccess')}
                        </Badge>
                      ) : (
                        // 失败 destructive 色，其余列不整行染色（specs §4.1.5）
                        <Badge variant="destructive">{t('list.resultFail')}</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="link"
                        size="sm"
                        onClick={() => setSelectedItem(item)}
                      >
                        {t('list.actionDetail')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            <div className="flex items-center justify-between">
              <span className="text-muted-foreground text-sm">
                {t('list.total', { total })}
              </span>
              <div className="flex items-center gap-2">
                <Select
                  value={String(filter.page_size)}
                  onValueChange={(v) => setFilter((f) => ({ ...f, page: 1, page_size: Number(v) }))}
                >
                  <SelectTrigger size="sm" aria-label={t('list.pageSizeLabel')}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PAGE_SIZE_OPTIONS.map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {t('list.pageSize', { n })}
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
                  {t('list.prevPage')}
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
                  {t('list.nextPage')}
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>

      <DetailDialog item={selectedItem} onClose={() => setSelectedItem(null)} />
    </Card>
  );
}
