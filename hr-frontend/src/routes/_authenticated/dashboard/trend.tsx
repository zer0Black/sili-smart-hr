// 能力逐期趋势页（specs P2_TMD_001 §4.2.1）：
// 顶部条（返回看板 + 能力类型 tab）+ 综合分摘要卡 + 维度分面卡网格 + 变化表。
// URL search.type 承载能力类型（直链与分享，§4.2.3），非法值 normalizeAbilityType 按 use 兜底；
// tab 切换 navigate 更新 URL 触发整体刷新（新 queryKey），期间整页加载态（§4.2.5）。
import {
  createFileRoute,
  useNavigate,
  useRouter,
  useSearch,
} from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, Loader2 } from 'lucide-react';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { TrendChangeTable } from '@/features/dashboard/components/trend-change-table';
import { TrendDimensionCard } from '@/features/dashboard/components/trend-dimension-card';
import { useDashboardTrend } from '@/features/dashboard/hooks';
import { changeColorClass, normalizeAbilityType, type DashboardAbilityType } from '@/features/dashboard/types';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_authenticated/dashboard/trend')({
  validateSearch: (search: Record<string, unknown>): { type?: string } => ({
    type: typeof search.type === 'string' && search.type !== '' ? search.type : undefined,
  }),
  component: DashboardTrendPage,
});

/** tab 顺序即渲染顺序（use 在前）。 */
const ABILITY_TABS: DashboardAbilityType[] = ['use', 'manage'];

/** 类型 → tab 标题键（复用 module 键：AI 使用能力 / AI 管理能力）。 */
const TAB_LABEL_KEY: Record<DashboardAbilityType, string> = {
  use: 'module.AI_USAGE',
  manage: 'module.AI_MGMT',
};

/** 数据来源说明（specs §4.2.2 A）：使用「对话分析」、管理「主动测试场景题」。 */
const SOURCE_KEY: Record<DashboardAbilityType, string> = {
  use: 'trend.sourceUsage',
  manage: 'trend.sourceMgmt',
};

function TrendLoadingBlock(): JSX.Element {
  const { t } = useTranslation('dashboard');
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-24" role="status">
      <Loader2 className="text-muted-foreground size-6 animate-spin" aria-hidden />
      <span className="text-muted-foreground text-sm">{t('page.loading')}</span>
    </div>
  );
}

export function DashboardTrendPage(): JSX.Element {
  const { t } = useTranslation('dashboard');
  const search = useSearch({ from: '/_authenticated/dashboard/trend' });
  const router = useRouter();
  const navigate = useNavigate();

  const ability = normalizeAbilityType(search.type);
  const query = useDashboardTrend(ability);
  const data = query.data;

  /** 返回看板（specs §4.2.3）：浏览器历史返回，无历史记录跳看板页兜底。 */
  function goBack() {
    if (window.history.length > 1) void router.history.back();
    else void navigate({ to: '/dashboard' });
  }

  /** 能力类型切换（specs §4.2.3）：URL 参数同步更新支持直链与分享。 */
  function switchType(type: DashboardAbilityType) {
    if (type === ability) return;
    void navigate({ to: '/dashboard/trend', search: { type } });
  }

  if (query.isError) {
    // 趋势接口失败整页错误占位 + 重试，不用部分数据降级渲染（specs §4.2.5）
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-24">
        <p className="text-muted-foreground text-sm">{t('trend.loadError')}</p>
        <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
          {t('page.retry')}
        </Button>
      </div>
    );
  }

  if (!data) {
    // 首次加载与 tab 切换整体刷新共用（新 queryKey pending，specs §4.2.5）
    return (
      <div className="flex flex-col gap-4">
        <TopBar ability={ability} onBack={goBack} onSwitch={switchType} />
        <TrendLoadingBlock />
      </div>
    );
  }

  // 空态（specs §4.2.5）：composite=null 且 dimensions=[] 整页提示 + 返回看板入口
  if (data.composite === null && data.dimensions.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <TopBar ability={ability} onBack={goBack} onSwitch={switchType} />
        <div className="flex flex-col items-center justify-center gap-3 py-24 text-center">
          <p className="text-muted-foreground text-sm">{t('trend.empty')}</p>
          <Button variant="outline" size="sm" onClick={goBack}>
            <ArrowLeft className="size-4" aria-hidden />
            {t('trend.back')}
          </Button>
        </div>
      </div>
    );
  }

  const composite = data.composite;
  // 观察窗口：走势序列覆盖的区间范围（specs §4.2.2 A，periods 旧到新）
  const windowLabel =
    data.periods.length > 0
      ? `${data.periods[0].period_start} ~ ${data.periods[data.periods.length - 1].period_end}`
      : '-';
  const currentLabel = data.current_period
    ? `${data.current_period.period_start} ~ ${data.current_period.period_end}`
    : '-';

  return (
    <div className="flex flex-col gap-6">
      <TopBar ability={ability} onBack={goBack} onSwitch={switchType} />

      {/* 综合分摘要卡（specs §4.2.2 A）：主指标 + 较上期变化方向色 + 观察窗口元信息 */}
      <Card className="gap-4 py-5">
        <CardContent className="flex flex-wrap items-end gap-x-10 gap-y-4 px-6">
          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground text-xs">{t('trend.compositeLabel')}</span>
            <span className="font-mono text-3xl font-semibold">
              {composite ? composite.score : '-'}
            </span>
            {composite && composite.change_vs_prev !== null ? (
              <span
                className={cn(
                  'inline-flex items-center gap-0.5 text-xs font-medium',
                  changeColorClass(composite.change_vs_prev),
                )}
              >
                {composite.change_vs_prev > 0
                  ? `+${composite.change_vs_prev}`
                  : `${composite.change_vs_prev}`}
                <span className="text-muted-foreground font-normal">
                  {t('trend.vsPrev')}
                </span>
              </span>
            ) : null}
          </div>
          <dl className="text-muted-foreground flex flex-wrap items-center gap-x-8 gap-y-2 text-xs">
            <div className="flex items-center gap-1.5">
              <dt>{t('trend.currentPeriod')}</dt>
              <dd className="text-foreground font-mono font-medium">{currentLabel}</dd>
            </div>
            <div className="flex items-center gap-1.5">
              <dt>{t('trend.dataSource')}</dt>
              <dd className="text-foreground font-medium">{t(SOURCE_KEY[ability])}</dd>
            </div>
            <div className="flex items-center gap-1.5">
              <dt>{t('trend.dimensionCount')}</dt>
              <dd className="text-foreground font-mono font-medium">
                {composite ? composite.dimension_count : '-'}
              </dd>
            </div>
            <div className="flex items-center gap-1.5">
              <dt>{t('trend.observationWindow')}</dt>
              <dd className="text-foreground font-mono font-medium">{windowLabel}</dd>
            </div>
            {data.data_updated_at ? (
              <div className="flex items-center gap-1.5">
                <dt>{t('trend.updatedAt')}</dt>
                <dd className="text-foreground font-mono font-medium">
                  {data.data_updated_at}
                </dd>
              </div>
            ) : null}
          </dl>
        </CardContent>
      </Card>

      {/* 维度分面卡网格（specs §4.2.2 B）：走势序列含全部维度 */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {data.dimensions.map((d) => (
          <TrendDimensionCard key={d.dimension_code} data={d} />
        ))}
      </div>

      <TrendChangeTable dimensions={data.dimensions} />
    </div>
  );
}

/** 顶部条：左返回看板，右能力类型 tab（specs §4.2.3）。 */
function TopBar(props: {
  ability: DashboardAbilityType;
  onBack: () => void;
  onSwitch: (type: DashboardAbilityType) => void;
}): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { ability, onBack, onSwitch } = props;

  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <Button variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeft className="size-4" aria-hidden />
        {t('trend.back')}
      </Button>
      <div role="tablist" aria-label={t('trend.typeLabel')} className="flex items-center gap-1">
        {ABILITY_TABS.map((tab) => (
          <button
            key={tab}
            type="button"
            role="tab"
            aria-selected={ability === tab}
            onClick={() => onSwitch(tab)}
            className={cn(
              'cursor-pointer rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
              ability === tab
                ? 'bg-primary/10 text-primary'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {t(TAB_LABEL_KEY[tab])}
          </button>
        ))}
      </div>
    </div>
  );
}
