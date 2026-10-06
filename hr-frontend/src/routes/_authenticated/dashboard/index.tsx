import { createFileRoute, Link } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { ActivityOverview } from '@/features/dashboard/components/activity-overview';
import { EnneagramPanel } from '@/features/dashboard/components/enneagram-panel';
import { ModuleRadarCard } from '@/features/dashboard/components/module-radar-card';
import { PeriodToolbar } from '@/features/dashboard/components/period-toolbar';
import { SuggestionPanel } from '@/features/dashboard/components/suggestion-panel';
import { useDashboardOverview } from '@/features/dashboard/hooks';
import type { DashboardOverview, DashboardPeriodItem } from '@/lib/contracts';

export const Route = createFileRoute('/_authenticated/dashboard/')({
  component: DashboardPage,
});

/** 数据区加载态：首次加载与切区间共用，旧区间数据不残留（specs §4.1.4 规则1）。 */
function LoadingBlock(): JSX.Element {
  const { t } = useTranslation('dashboard');
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-24" role="status">
      <Loader2 className="text-muted-foreground size-6 animate-spin" aria-hidden />
      <span className="text-muted-foreground text-sm">{t('page.loading')}</span>
    </div>
  );
}

/** 团队看板页（specs P2_TMD_001 §4.1.1）：区间工具条 + 活跃度概览 + 两模块雷达卡 +
 *  九型构成 + 培训建议。shell 保存最近成功 overview：切区间（新 queryKey pending）
 *  期间工具条常驻、数据区整块加载态防新旧混渲染（§4.1.4 规则1）。 */
export function DashboardPage(): JSX.Element {
  const { t } = useTranslation('dashboard');
  // null = 最新区间（§4.1.3 页面加载自动查询按默认区间发起）
  const [selected, setSelected] = useState<DashboardPeriodItem | null>(null);
  // 最近一次成功 overview：承载切换期间常驻的区间下拉选项
  const [shell, setShell] = useState<DashboardOverview | null>(null);

  const query = useDashboardOverview(selected ?? undefined);

  useEffect(() => {
    if (query.data) setShell(query.data);
  }, [query.data]);

  const data = query.data;
  // 仅判 isPending（首次加载与切区间新 queryKey）：后台静默刷新（isFetching）
  // 保持已可见数据，刷新完成后自然替换，防重访闪整块加载态。
  const loading = query.isPending;

  if (query.isError) {
    // 整页接口失败（含 1305）错误占位 + 重试，不用部分数据降级渲染（specs §4.1.5）
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-24">
        <p className="text-muted-foreground text-sm">{t('page.loadError')}</p>
        <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
          {t('page.retry')}
        </Button>
      </div>
    );
  }

  if (!data && !shell) {
    // 首次加载：整页加载态
    return <LoadingBlock />;
  }

  // 空态语义（§4.1.5）：无任何落库区间时 periods 为空数组，整页空态 + 评测运营中心入口
  const source = data ?? shell!;
  if (source.periods.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-8 text-center">
        <p className="text-muted-foreground text-sm">{t('page.empty')}</p>
        <Button asChild variant="outline" size="sm">
          <Link to="/assessment">{t('page.emptyGo')}</Link>
        </Button>
      </div>
    );
  }

  // 工具条区间显示：优先响应对齐（selected_period），切换期间回退本地 selected 防跳回
  const period = data?.selected_period ?? selected ?? shell?.selected_period ?? null;
  const current = data ?? shell!;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

      {/* 区间工具条常驻：切区间期间仅数据区换加载态（specs §4.1.4 规则1） */}
      <PeriodToolbar
        periods={current.periods}
        selected={period}
        dataUpdatedAt={current.data_updated_at}
        onSelect={setSelected}
      />

      {loading ? (
        <LoadingBlock />
      ) : (
        <>
          {current.activity ? (
            <ActivityOverview data={current.activity} staffTotal={current.staff_total} />
          ) : null}
          {/* 两模块雷达卡（恒两项）：活跃度与雷达随所选区间整体刷新（specs §4.1.3） */}
          <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
            {current.modules.map((m) => (
              <ModuleRadarCard key={m.module} data={m} />
            ))}
          </div>
          {/* 九型与建议：同一响应承载最新快照，不随区间回看（specs §4.1.4 规则5/规则6） */}
          <EnneagramPanel data={current.enneagram} />
          <SuggestionPanel data={current.suggestion} />
        </>
      )}
    </div>
  );
}
