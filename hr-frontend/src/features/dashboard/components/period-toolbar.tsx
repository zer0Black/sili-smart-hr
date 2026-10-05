// 评估区间工具条（specs P2_TMD_001 §4.1.2 A / §4.1.3 评估区间切换）：
// 区间下拉（新到旧，is_current 项追加「（本期）」）+ 数据更新时间文本。
// 区间集合由后端三表落库周期推导（仅列已落库区间），组件按 is_current 标注不自行推断。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { DashboardPeriodItem } from '@/lib/contracts';

/** 区间 value 编码：start~end。日期为 yyyy-MM-dd 无波浪号冲突。 */
function periodValue(p: DashboardPeriodItem): string {
  return `${p.period_start}~${p.period_end}`;
}

export function PeriodToolbar(props: {
  periods: DashboardPeriodItem[];
  selected: DashboardPeriodItem | null;
  dataUpdatedAt: string | null;
  onSelect: (p: DashboardPeriodItem) => void;
}): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { periods, selected, dataUpdatedAt, onSelect } = props;
  const value = selected ? periodValue(selected) : undefined;

  return (
    <div className="flex flex-wrap items-center gap-4">
      <Select
        value={value}
        onValueChange={(v) => {
          const hit = periods.find((p) => periodValue(p) === v);
          if (hit) onSelect(hit);
        }}
      >
        <SelectTrigger className="w-64" aria-label={t('toolbar.periodLabel')}>
          <SelectValue placeholder={t('toolbar.periodPlaceholder')} />
        </SelectTrigger>
        <SelectContent>
          {periods.map((p) => (
            <SelectItem key={periodValue(p)} value={periodValue(p)}>
              {p.period_start} ~ {p.period_end}
              {p.is_current ? `（${t('toolbar.current')}）` : ''}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {dataUpdatedAt ? (
        <span className="text-muted-foreground text-xs">
          {t('toolbar.updatedAt', { time: dataUpdatedAt })}
        </span>
      ) : null}
    </div>
  );
}
