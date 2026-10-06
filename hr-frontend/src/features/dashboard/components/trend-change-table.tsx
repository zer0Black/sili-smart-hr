// 本期较上期变化表（specs P2_TMD_001 §4.2.2 C / §4.2.4 规则2/规则3）：
// 四列表格（维度[含色点与短板标签]/上期分/本期分/变化），固定行数无分页（specs §8.3 偏离记录）。
// prev/current/change 任一 null 显示 -；变化非 null 按升绿降红持平灰着色。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { changeColorClass } from '@/features/dashboard/types';
import type { DashboardTrendDim } from '@/lib/contracts';
import { cn } from '@/lib/utils';

export function TrendChangeTable(props: {
  dimensions: DashboardTrendDim[];
}): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { dimensions } = props;

  const changeText = (v: number) => (v > 0 ? `+${v}` : `${v}`);
  const changeClass = changeColorClass;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('trend.changeTitle')}</CardTitle>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('trend.colDimension')}</TableHead>
              <TableHead className="text-right">{t('trend.colPrev')}</TableHead>
              <TableHead className="text-right">{t('trend.colCurrent')}</TableHead>
              <TableHead className="text-right">{t('trend.colChange')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {dimensions.map((d) => (
              <TableRow key={d.dimension_code}>
                <TableCell>
                  <span className="flex items-center gap-2">
                    {/* 色点：短板橙、常规主题色（specs §4.2.2 C 本期共性短板口径） */}
                    <span
                      aria-hidden
                      className={cn(
                        'inline-block size-2 shrink-0 rounded-full',
                        d.is_weakness ? 'bg-warning' : 'bg-primary',
                      )}
                    />
                    <span className="font-medium">{d.dimension_name}</span>
                    {d.is_weakness ? (
                      <span className="rounded-sm bg-warning/15 px-1.5 py-0.5 text-xs font-medium text-warning">
                        {t('trend.weaknessTag')}
                      </span>
                    ) : null}
                  </span>
                </TableCell>
                <TableCell className="text-right font-mono">
                  {d.prev_score === null ? '-' : d.prev_score}
                </TableCell>
                <TableCell className="text-right font-mono">
                  {d.current_score === null ? '-' : d.current_score}
                </TableCell>
                <TableCell
                  className={cn(
                    'text-right font-mono',
                    // 任一期缺失（null）显示 - 不着色（specs §4.2.2 C）
                    d.change === null ? '' : changeClass(d.change),
                  )}
                >
                  {d.change === null ? '-' : changeText(d.change)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
