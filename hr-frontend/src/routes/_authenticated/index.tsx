import { createFileRoute } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { AttentionTable } from '@/features/workspace/components/attention-table';
import { BatchSituationCards } from '@/features/workspace/components/batch-situation-cards';
import { ProfileBrief } from '@/features/workspace/components/profile-brief';
import { TrendEvolution } from '@/features/workspace/components/trend-evolution';
import { useWorkspace } from '@/features/workspace/hooks';
import type { WorkspaceModuleCode } from '@/features/workspace/types';

export const Route = createFileRoute('/_authenticated/')({
  component: WorkspacePage,
});

/** 整页骨架占位：isPending 期间四区块骨架（specs §4.1.3 前端自动流程）。 */
function PageSkeleton(): JSX.Element {
  const { t } = useTranslation('workspace');
  return (
    <div className="flex flex-col gap-6" role="status" aria-label={t('page.loading')}>
      <Loader2 className="text-muted-foreground size-6 animate-spin" aria-hidden />
      <div className="bg-muted h-28 w-full animate-pulse rounded-xl" />
      <div className="bg-muted h-72 w-full animate-pulse rounded-xl" />
      <div className="bg-muted h-96 w-full animate-pulse rounded-xl" />
      <div className="bg-muted h-56 w-full animate-pulse rounded-xl" />
    </div>
  );
}

/** 工作台首页（specs P2_WRK_001 §4.1.1）：登录后默认落地，自上而下四区块：
 *  态势与待处理统计卡区 → 团队能力演进 → 团队整体画像速览 → 需要关注的人。
 *  区块级空态互不阻断（specs §4.1.4 规则4）；无轮询（进行中批次经跳转跟进）。 */
export function WorkspacePage(): JSX.Element {
  const { t } = useTranslation('workspace');
  const query = useWorkspace();

  if (query.isError) {
    // 整页错误占位 + 重试（同 dashboard 页形态）
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-24">
        <p className="text-muted-foreground text-sm">{t('page.loadError')}</p>
        <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
          {t('page.retry')}
        </Button>
      </div>
    );
  }

  if (query.isPending) return <PageSkeleton />;

  const data = query.data;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

      <BatchSituationCards data={data.batch} />

      {data.trend ? (
        <TrendEvolution data={data.trend} />
      ) : (
        <SectionEmpty title={t('trend.title')} />
      )}

      {data.profile ? (
        <ProfileBrief data={data.profile} />
      ) : (
        <SectionEmpty title={t('profile.title')} />
      )}

      <AttentionTable
        rows={data.attention ?? []}
        formatModule={formatModuleLabel}
        formatDimension={formatDimensionLabel}
      />
    </div>
  );
}

/** 区块级空态：标题保留 + 暂无数据提示（specs §4.1.4 规则4，独立空态不阻断整页）。 */
function SectionEmpty(props: { title: string }): JSX.Element {
  const { t } = useTranslation('workspace');
  return (
    <div className="flex flex-col items-center justify-center gap-2 rounded-xl border p-8">
      <h2 className="text-sm font-semibold">{props.title}</h2>
      <p className="text-muted-foreground text-sm">{t('page.sectionEmpty')}</p>
    </div>
  );
}

/** 模块名：与 dashboard:module 口径一致。 */
function formatModuleLabel(m: WorkspaceModuleCode): string {
  const { t } = useTranslation('dashboard');
  return t(`module.${m}`);
}

/** 维度名：attention 行 weak_dims 为 code 集合，无集中维度名映射时回退 code 原值。 */
function formatDimensionLabel(code: string): string {
  return code;
}
