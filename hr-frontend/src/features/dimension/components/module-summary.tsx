// 维度配置页右侧：模块维度汇总视图。只读表格 + 权重合计进度条。
import { useTranslation } from 'react-i18next';

import { Check, TriangleAlert } from 'lucide-react';
import type { DimensionBrief } from '@/lib/contracts';
import type { DataSource, ModuleCode } from '@/features/dimension/types';
import { MODULE_META } from '@/features/dimension/types';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';

export interface ModuleSummaryProps {
  /** 该模块下所有叶子维度（非 AI_USAGE 是 module.dimensions；AI_USAGE 由父组件聚合各分组）。 */
  dimensions: DimensionBrief[];
  moduleCode: ModuleCode;
}

/**
 * 计算参与总览且启用的维度权重合计（specs §4.1.4 规则2）。
 * 仅 include_overview=true 与 enabled=true 的维度计入分母分子。
 */
function sumOverviewWeight(list: readonly DimensionBrief[]): number {
  return list
    .filter((d) => d.include_overview && d.enabled)
    .reduce((acc, d) => acc + d.weight, 0);
}

/** 权重合计进度条与状态提示。 */
function WeightTotal({
  total,
  hidden,
}: {
  total: number;
  hidden: boolean;
}) {
  const { t } = useTranslation('dimension');
  if (hidden) {
    return (
      <div className="text-muted-foreground text-sm">
        {t('summary.referenceHint')}
      </div>
    );
  }

  const clamped = Math.min(100, Math.max(0, total));
  const isComplete = total === 100;
  const delta = total - 100;

  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2 text-sm">
        <span className="text-muted-foreground">{t('summary.weightTotal')}</span>
        <span
          className={cn(
            'font-medium',
            isComplete ? 'text-green-600' : 'text-yellow-600',
          )}
        >
          {total}%
        </span>
        {isComplete ? (
          <span className="inline-flex items-center gap-1 text-green-600">
            <Check className="h-4 w-4" />
            <span className="text-sm">{t('summary.aligned')}</span>
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-yellow-600">
            <TriangleAlert className="h-4 w-4" />
            <span className="text-sm">
              {t('summary.deviation', { delta: delta > 0 ? `+${delta}` : `${delta}` })}
            </span>
          </span>
        )}
      </div>
      <div
        className="h-1.5 w-full overflow-hidden rounded-xs bg-muted"
        role="progressbar"
        aria-valuenow={clamped}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className={cn(
            'h-full rounded-xs transition-all',
            isComplete ? 'bg-green-600' : 'bg-yellow-500',
          )}
          style={{ width: `${clamped}%` }}
        />
      </div>
    </div>
  );
}

/**
 * 模块维度汇总。只读，不可编辑（编辑走 T3 维度详情/编辑面板）。
 * ACTIVITY 模块由父组件切换为规则面板，不会渲染本组件。
 */
export function ModuleSummary({
  dimensions,
  moduleCode,
}: ModuleSummaryProps) {
  const { t } = useTranslation('dimension');
  const meta = MODULE_META[moduleCode];
  const total = sumOverviewWeight(dimensions);
  const isReference = meta.isReference;
  const dataSourceLabel = (ds: string) =>
    t(`dataSource.${ds}`, { defaultValue: ds });

  return (
    <div className="space-y-4">
      <WeightTotal total={total} hidden={isReference} />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12 text-right">{t('summary.colIndex')}</TableHead>
              <TableHead>{t('summary.colName')}</TableHead>
              <TableHead>{t('summary.colDataSource')}</TableHead>
              <TableHead className="text-right">{t('summary.colWeight')}</TableHead>
              <TableHead>{t('summary.colOverview')}</TableHead>
              <TableHead>{t('summary.colStatus')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {dimensions.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="text-muted-foreground py-6 text-center text-sm"
                >
                  {t('summary.empty')}
                </TableCell>
              </TableRow>
            ) : (
              dimensions.map((dim, idx) => (
                <TableRow key={dim.id}>
                  <TableCell className="text-right font-mono text-muted-foreground text-xs">
                    {idx + 1}
                  </TableCell>
                  <TableCell className="font-medium">{dim.name}</TableCell>
                  <TableCell className="text-muted-foreground text-sm">
                    {dataSourceLabel(dim.data_source as DataSource)}
                  </TableCell>
                  <TableCell className="text-right font-mono text-sm">
                    {dim.include_overview ? `${dim.weight}%` : '—'}
                  </TableCell>
                  <TableCell className="text-sm">
                    {dim.include_overview ? t('summary.overviewYes') : t('summary.overviewNo')}
                  </TableCell>
                  <TableCell>
                    <Badge variant={dim.enabled ? 'default' : 'secondary'}>
                      {dim.enabled ? t('summary.statusEnabled') : t('summary.statusDisabled')}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
