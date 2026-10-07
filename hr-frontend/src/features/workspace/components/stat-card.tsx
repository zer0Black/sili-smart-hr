// 工作台域共用统计卡（specs P2_WRK_001 §4.1.2 A/B）：label + 主数值 + 可选环比区段
// 或副提示；可点击变体 hover 边框/底色形态，与 dashboard activity-overview StatCard 同风格。
import type { JSX } from 'react';

type StatCardProps = {
  label: string;
  value: number | string;
  /** 环比区段节点（ChangeBadge）。 */
  change?: JSX.Element | null;
  /** 副提示文案。 */
  hint?: string;
};

/** 静态统计卡：不可点击。 */
export function StatCard(props: StatCardProps): JSX.Element {
  const { label, value, change } = props;
  return (
    <div className="rounded-lg border p-4">
      <span className="block text-xs font-medium">{label}</span>
      <span className="mt-1 flex items-baseline gap-2">
        <span className="font-mono text-2xl font-semibold">{value}</span>
        {change ?? null}
      </span>
    </div>
  );
}

/** 可点击统计卡：整卡可点，承载跳转入口。 */
export function ClickableStatCard(
  props: StatCardProps & { onClick: () => void; ariaLabel?: string },
): JSX.Element {
  const { label, value, change, hint, onClick, ariaLabel } = props;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={ariaLabel ?? label}
      className="cursor-pointer rounded-lg border p-4 text-left transition-colors hover:border-primary/40 hover:bg-accent/50"
    >
      <span className="block text-xs font-medium">{label}</span>
      <span className="mt-1 flex items-baseline gap-2">
        <span className="font-mono text-2xl font-semibold">{value}</span>
        {change ?? null}
      </span>
      {hint ? <span className="text-muted-foreground mt-1 block text-xs">{hint}</span> : null}
    </button>
  );
}
