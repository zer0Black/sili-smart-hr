// 人员画像列表表格（specs P2_PRF_001 §4.1）：筛选区（姓名/活跃度/短板维度/仅看未使用）
// + 工具栏（导出）+ 表格 + 分页。筛选与分页状态组件内部自治（batch-table 样板）。
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';
import { toast } from 'sonner';
import { Loader2, TriangleAlert } from 'lucide-react';
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
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useDimensionTree } from '@/features/dimension/hooks';
import type { ProfileListItem } from '@/lib/contracts';
import { usePageClamp, useResetSignal } from '@/lib/use-table-state';

import { useProfileExport, useProfileList } from '../hooks';
import { activityLevelKey, enneagramTypeKey, roundScore } from '../status';
import type { ProfileFilter } from '@/lib/contracts';

/** 表格默认每页条数（specs §4.1.5：默认 10 条/页）。 */
const DEFAULT_PAGE_SIZE = 10;
// 通用分页可选 10/20/30（specs §4.1.5 / §8.3）
const PAGE_SIZE_OPTIONS = [10, 20, 30];
const ALL = '__all__';

/** 模块分数单元格：null 显示「待评估」（specs §4.1.4 规则2）；降权旁带警示图标 + 悬浮说明（规则3，
 *  两模块说明文案不同；警示只标注不改分值）。 */
function ScoreCell(props: { score: number | null; degraded: boolean; warnKey: string }): JSX.Element {
  const { score, degraded, warnKey } = props;
  const { t } = useTranslation('profile');
  if (score === null) {
    return <span className="text-muted-foreground text-sm">{t('list.pendingScore')}</span>;
  }
  return (
    <span className="inline-flex items-center gap-1" title={degraded ? t(warnKey) : undefined}>
      {roundScore(score)}
      {degraded && <TriangleAlert className="size-3.5 text-warning" aria-label={t(warnKey)} />}
    </span>
  );
}

/** 人员画像列表表格：筛选区（姓名/活跃度/短板维度/仅看未使用）+ 工具栏（导出）+ 表格 + 分页。
 *  筛选与分页状态由组件内部自治（仿 batch-table）；父级 resetKey 重置回第一页；
 *  initialFilter 首挂载预填筛选（看板深链，specs §4.1.3 / §7.2）。 */
export function ProfileTable(props: {
  resetKey?: number;
  initialFilter?: { dimension_code?: string; unused_only?: boolean };
}): JSX.Element {
  const { t } = useTranslation('profile');
  const navigate = useNavigate();

  // 姓名是草稿态（回车/查询才提交，specs §4.1.3）；下拉与开关即时生效直接写 filter
  const [draftName, setDraftName] = useState('');
  const [filter, setFilter] = useState<ProfileFilter>({
    page: 1,
    page_size: DEFAULT_PAGE_SIZE,
    dimension_code: props.initialFilter?.dimension_code,
    unused_only: props.initialFilter?.unused_only ? true : undefined,
  });

  const query = useProfileList(filter);
  const exportMut = useProfileExport();

  // 短板维度下拉：启用且 module ∈ {AI_USAGE, AI_MGMT} 维度并集（specs §4.1.2 A）
  const treeQ = useDimensionTree();
  const dimensionOptions = (treeQ.data?.modules ?? [])
    .filter((m) => m.module_code === 'AI_USAGE' || m.module_code === 'AI_MGMT')
    .flatMap((m) => (m.groups ? m.groups.flatMap((g) => g.dimensions) : (m.dimensions ?? [])))
    .filter((d) => d.enabled);

  // 深链预填值域校验（specs P2_TMD_001 §7.2）：维度树就绪后校验 dimension_code，
  // 无效 code（手改 URL）清空筛选按 undefined 处理，避免空下拉挂死与无谓请求。
  const initialDim = props.initialFilter?.dimension_code;
  useEffect(() => {
    if (initialDim && treeQ.data && !dimensionOptions.some((d) => d.code === initialDim)) {
      setFilter((f) => (f.dimension_code === initialDim ? { ...f, dimension_code: undefined } : f));
    }
  }, [initialDim, treeQ.data, dimensionOptions]);

  // 外部 resetKey（跳过首挂载）：清筛选回第一页，filter 变化触发重查
  useResetSignal(props.resetKey, () => {
    setDraftName('');
    setFilter({ page: 1, page_size: DEFAULT_PAGE_SIZE });
  });

  const list = query.data?.list ?? [];
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / filter.page_size));
  // 页码钳位：total 收缩让当前页落空时回落末页（batch-table 先例）
  usePageClamp(filter.page, totalPages, (page) => setFilter((f) => ({ ...f, page })));

  // 有筛选条件时空态文案不同（specs §4.1.5）
  const hasFilter = !!(filter.name || filter.activity_level || filter.dimension_code || filter.unused_only);

  function onQuery() {
    setFilter((f) => ({ ...f, name: draftName.trim() === '' ? undefined : draftName.trim(), page: 1 }));
    if (query.isError) void query.refetch();
  }

  function onReset() {
    setDraftName('');
    setFilter({ page: 1, page_size: filter.page_size });
  }

  /** 即时筛选（specs §4.1.3）：下拉/开关变更直接落 filter 并回第 1 页。 */
  function applyImmediate(patch: Partial<ProfileFilter>) {
    setFilter((f) => ({ ...f, ...patch, page: 1 }));
  }

  /** 整行与查看画像共用跳转（specs §4.1.5）：依赖 Router 对 path params 默认编码，不手动编码。 */
  function goDetail(staffName: string) {
    void navigate({
      to: '/profile/$staffName',
      params: { staffName },
    });
  }

  function onExport() {
    // 按当前筛选条件导出（specs §4.1.3）：与列表已生效的 filter 一致，不含未查询的姓名草稿
    exportMut.mutate(
      {
        name: filter.name,
        activity_level: filter.activity_level,
        dimension_code: filter.dimension_code,
        unused_only: filter.unused_only,
      },
      {
        // 导出失败通用错误文案，不产出文件（specs §4.1.3）
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
            value={draftName}
            onChange={(e) => setDraftName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onQuery();
            }}
            placeholder={t('list.namePlaceholder')}
            aria-label={t('list.nameLabel')}
            className="w-48"
          />
          <Select
            value={filter.activity_level ?? ALL}
            onValueChange={(v) => applyImmediate({ activity_level: v === ALL ? undefined : v })}
          >
            <SelectTrigger className="w-32" aria-label={t('list.filterActivity')}>
              <SelectValue placeholder={t('list.filterActivityAll')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('list.filterActivityAll')}</SelectItem>
              <SelectItem value="active">{t('activityLevel.active')}</SelectItem>
              <SelectItem value="low_freq">{t('activityLevel.low_freq')}</SelectItem>
              <SelectItem value="unused">{t('activityLevel.unused')}</SelectItem>
            </SelectContent>
          </Select>
          <Select
            value={filter.dimension_code ?? ALL}
            onValueChange={(v) => applyImmediate({ dimension_code: v === ALL ? undefined : v })}
          >
            <SelectTrigger className="w-40" aria-label={t('list.filterDimension')}>
              <SelectValue placeholder={t('list.filterDimensionAll')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>{t('list.filterDimensionAll')}</SelectItem>
              {dimensionOptions.map((d) => (
                <SelectItem key={d.id} value={d.code}>
                  {d.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <label className="flex items-center gap-1.5 text-sm">
            <Switch
              checked={!!filter.unused_only}
              onCheckedChange={(v) => applyImmediate({ unused_only: v || undefined })}
              aria-label={t('list.unusedOnly')}
            />
            {t('list.unusedOnly')}
          </label>
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
          // 上游不可用错误态：错误占位 + 重试（specs §4.1.5），不展示降级名单（§4.1.4 规则1）
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
                  <TableHead>{t('list.colName')}</TableHead>
                  <TableHead>{t('list.colActivity')}</TableHead>
                  <TableHead>{t('list.colAiUsage')}</TableHead>
                  <TableHead>{t('list.colAiMgmt')}</TableHead>
                  <TableHead>{t('list.colEnneagram')}</TableHead>
                  <TableHead className="text-right">{t('list.colActions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((item: ProfileListItem) => (
                  // 整行可点击等价查看画像（specs §4.1.5）
                  <TableRow
                    key={item.staff_name}
                    className="cursor-pointer"
                    onClick={() => goDetail(item.staff_name)}
                  >
                    <TableCell className="font-medium">{item.staff_name}</TableCell>
                    <TableCell>
                      <Badge variant={item.activity_level === 'unused' ? 'secondary' : 'default'}>
                        {t(activityLevelKey[item.activity_level])}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <ScoreCell
                        score={item.ai_usage_score}
                        degraded={item.ai_usage_degraded}
                        warnKey="list.degradedUsage"
                      />
                    </TableCell>
                    <TableCell>
                      <ScoreCell
                        score={item.ai_mgmt_score}
                        degraded={item.ai_mgmt_degraded}
                        warnKey="list.degradedMgmt"
                      />
                    </TableCell>
                    <TableCell>
                      {item.enneagram_main_type === null ? (
                        <span className="text-muted-foreground">-</span>
                      ) : enneagramTypeKey[item.enneagram_main_type] ? (
                        t(enneagramTypeKey[item.enneagram_main_type])
                      ) : (
                        item.enneagram_main_type
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="link"
                        size="sm"
                        onClick={(e) => {
                          e.stopPropagation();
                          goDetail(item.staff_name);
                        }}
                      >
                        {t('list.actionView')}
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
    </Card>
  );
}
