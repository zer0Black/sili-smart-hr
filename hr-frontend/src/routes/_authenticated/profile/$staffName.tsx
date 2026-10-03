import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ArrowLeft, Loader2 } from 'lucide-react';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { fetchDimensionTree } from '@/features/dimension/api';
import { ConclusionPanel } from '@/features/profile/components/conclusion-panel';
import { DimensionPanel } from '@/features/profile/components/dimension-panel';
import { EnneagramPanel } from '@/features/profile/components/enneagram-panel';
import { PeriodSelect } from '@/features/profile/components/period-select';
import { SummaryCards } from '@/features/profile/components/summary-cards';
import { useProfileDetail } from '@/features/profile/hooks';
import { activityLevelKey, enneagramTypeKey } from '@/features/profile/status';
import type { ProfileTabKey } from '@/features/profile/types';
import type { ProfileDetail, ProfilePeriodRange } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { useAuthStore } from '@/stores/auth';

export const Route = createFileRoute('/_authenticated/profile/$staffName')({
  component: ProfileDetailRoute,
});

function ProfileDetailRoute() {
  const { staffName } = Route.useParams();
  return <ProfileDetailPage staffName={staffName} />;
}

/** 所选区间无效（后端 2001，specs §5.2.4 规则1）：toast 提示并回落最新区间，不走整页占位。 */
const PROFILE_PERIOD_INVALID = 2001;

const EMPTY_TREE = { modules: [] };

/** 区间切换期间数据区加载态（顶部条常驻防抖动，specs §4.2.5）。 */
function LoadingBlock(): JSX.Element {
  const { t } = useTranslation('profile');
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-24" role="status">
      <Loader2 className="text-muted-foreground size-6 animate-spin" aria-hidden />
      <span className="text-muted-foreground text-sm">{t('detail.loading')}</span>
    </div>
  );
}

/** 个人画像详情页（specs §4.2.1）：概览条 + 结论 + 评分卡 + 双 tab 维度面板 + 九型参考。
 *  顶部条（返回/姓名/区间下拉）常驻：shell 保存最近一次成功 detail，区间切换（新 queryKey
 *  pending）期间数据区整块换加载态防新旧混渲染（specs §4.2.5），下拉禁用防抖动。 */
export function ProfileDetailPage(props: { staffName: string }): JSX.Element {
  const { staffName } = props;
  const { t } = useTranslation('profile');
  const router = useRouter();
  const navigate = useNavigate();
  const token = useAuthStore((s) => s.token);

  // selected 为 undefined = 最新区间（specs §5.2.2 流程 1：区间为空默认最新）
  const [selected, setSelected] = useState<ProfilePeriodRange | undefined>(undefined);
  const [activeTab, setActiveTab] = useState<ProfileTabKey>('aiUsage');
  // 管理雷达首次切入才渲染，此后保持挂载（specs §4.2.3 按需加载）
  const [mgmtMounted, setMgmtMounted] = useState(false);
  // 最近一次成功 detail：承载区间切换期间常驻的顶部条与下拉选项
  const [shell, setShell] = useState<ProfileDetail | null>(null);

  const query = useProfileDetail(staffName, selected);

  // 维度树：queryKey 与 dimension 域一致共享缓存（tab 计数与分组现读树，specs §4.2.3）
  const treeQ = useQuery({
    queryKey: ['dimension', 'tree'],
    queryFn: fetchDimensionTree,
    enabled: !!token,
  });
  const dimensionTree = treeQ.data ?? EMPTY_TREE;

  useEffect(() => {
    if (query.data) setShell(query.data);
  }, [query.data]);

  // 2001 单独分支（specs §5.2.4 规则1）：toast 提示后清 selected 回落最新区间重查
  useEffect(() => {
    const err = query.error;
    if (err instanceof ApiError && err.code === PROFILE_PERIOD_INVALID) {
      toast.error(t('detail.periodInvalid'));
      setSelected(undefined);
    }
  }, [query.error, t]);

  function onTabChange(tab: ProfileTabKey) {
    setActiveTab(tab);
    if (tab === 'aiMgmt') setMgmtMounted(true);
  }

  function goBack() {
    // 浏览器历史返回；无历史（如团队看板直入）回列表页兜底（specs §3.2）
    if (window.history.length > 1) void router.history.back();
    else void navigate({ to: '/profile' });
  }

  const detail = query.data;

  // 2001 已在 effect 中回落：pending/fetching/回落等待期由加载态承接，不进整页错误占位
  const periodInvalid =
    query.error instanceof ApiError && query.error.code === PROFILE_PERIOD_INVALID;
  const loading = query.isPending || query.isFetching || periodInvalid;

  if (query.isError && !periodInvalid) {
    // 1305 与网络错误：整页错误占位 + 重试，不用画像数据降级渲染（specs §4.2.5）
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-24">
        <p className="text-muted-foreground text-sm">{t('detail.loadError')}</p>
        <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
          {t('detail.retry')}
        </Button>
      </div>
    );
  }

  if (!detail && !shell) {
    // 首次加载（或 2001 回落且从未成功过）：整页加载态（specs §4.2.5）
    return <LoadingBlock />;
  }

  // 空画像语义（specs §5.2.4 规则1：查无落库周期行返回空 periods，前端进空态）
  const periodsSource = detail ?? shell!;
  if (periodsSource.periods.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-24">
        <p className="text-muted-foreground text-sm">{t('detail.empty')}</p>
        <Button variant="outline" size="sm" onClick={goBack}>
          <ArrowLeft className="size-4" aria-hidden />
          {t('detail.emptyBack')}
        </Button>
      </div>
    );
  }

  // 顶部条区间显示：优先响应对齐（selected_period），切换期间回退本地 selected 防跳回
  const period = detail?.selected_period ?? selected ?? shell?.selected_period ?? null;
  const periodLabel = period
    ? `${period.period_start} ~ ${period.period_end}${isCurrent(period, periodsSource.periods) ? `（${t('periodSelect.current')}）` : ''}`
    : '-';
  const activity = detail?.activity_level ?? shell!.activity_level;
  return (
    <div className="flex flex-col gap-6">
      {/* 顶部条：返回列表 + 姓名与活跃度标签 + 区间下拉（specs §4.2.3），切换期间常驻 */}
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="ghost" size="sm" onClick={goBack}>
          <ArrowLeft className="size-4" aria-hidden />
          {t('detail.back')}
        </Button>
        <h1 className="text-xl font-semibold tracking-tight">{staffName}</h1>
        <Badge variant={activity === 'unused' ? 'secondary' : 'default'}>
          {t(activityLevelKey[activity])}
        </Badge>
        <div className="ml-auto">
          {/* 区间切换期间禁用防抖动（specs §4.2.5） */}
          <PeriodSelect
            periods={periodsSource.periods}
            selected={period}
            onChange={setSelected}
            disabled={loading}
          />
        </div>
      </div>

      {loading ? (
        // 区间切换/回落期间数据区整体加载态，旧区间数据不残留（specs §4.2.5）
        <LoadingBlock />
      ) : (
        <>
          {/* 人物概览条（specs §4.2.2 A）：姓名在顶部条，此处承载活跃度/九型主型/当前区间 */}
          <dl className="text-muted-foreground grid grid-cols-3 gap-4 border-b pb-4 text-sm">
            <div>
              <dt className="text-xs">{t('overview.activityLabel')}</dt>
              <dd className="mt-1 text-foreground font-medium">
                {t(activityLevelKey[detail!.activity_level])}
              </dd>
            </div>
            <div>
              <dt className="text-xs">{t('overview.enneagramLabel')}</dt>
              <dd className="mt-1 text-foreground font-medium">
                {detail!.enneagram === null
                  ? t('overview.enneagramNone')
                  : enneagramTypeKey[detail!.enneagram.main_type]
                    ? t(enneagramTypeKey[detail!.enneagram.main_type])
                    : detail!.enneagram.main_type}
              </dd>
            </div>
            <div>
              <dt className="text-xs">{t('overview.periodLabel')}</dt>
              <dd className="mt-1 text-foreground font-medium">{periodLabel}</dd>
            </div>
          </dl>

          <ConclusionPanel detail={detail!} dimensionTree={dimensionTree} />
          <SummaryCards modules={detail!.modules} />
          <DimensionPanel
            detail={detail!}
            dimensionTree={dimensionTree}
            activeTab={activeTab}
            onTabChange={onTabChange}
            mgmtMounted={mgmtMounted}
            onMgmtFirstActivated={() => setMgmtMounted(true)}
          />
          {/* 九型区不随区间变化（specs §4.2.3）：数据为最新判型行，区间切换不重取 */}
          <EnneagramPanel enneagram={detail!.enneagram} />
        </>
      )}
    </div>
  );
}

/** 所选区间是否本期（specs §4.2.2 A 当前区间标注）。 */
function isCurrent(
  period: ProfilePeriodRange,
  periods: ProfileDetail['periods'],
): boolean {
  return periods.some(
    (p) => p.is_current && p.period_start === period.period_start && p.period_end === period.period_end,
  );
}
