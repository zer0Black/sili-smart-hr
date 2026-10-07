// 态势与待处理统计卡区（specs P2_WRK_001 §4.1.2 A / §4.1.3 跑批态势/告警/逾期卡跳转 / §4.1.4 规则4）：
// 批次状态 Badge（沿用 F6 列表四态口径）+ 下次跑批与数据更新时间（null 不渲染）+
// 三张卡片级可点击统计卡（hover 形态同 activity-overview StatCard）；status=null 整区空态引导。
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { ChevronRight } from 'lucide-react';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { WorkspaceBatch } from '@/lib/contracts';

/** 状态标签变体：running 中性、success 系统、partial/failed 警示（F6 batch-table 同款）。 */
function statusVariant(
  status: Exclude<WorkspaceBatch['status'], null>,
): 'default' | 'secondary' | 'destructive' {
  if (status === 'success') return 'secondary';
  if (status === 'partial_failed' || status === 'failed') return 'destructive';
  return 'default';
}

/** 可点击统计卡：label + 主数值 + 副提示，hover 边框/底色与 StatCard 形态一致。 */
function ClickableCard(props: {
  label: string;
  value: number | string;
  hint?: string;
  onClick: () => void;
}): JSX.Element {
  const { label, value, hint, onClick } = props;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="cursor-pointer rounded-lg border p-4 text-left transition-colors hover:border-primary/40 hover:bg-accent/50"
    >
      <span className="block text-xs font-medium">{label}</span>
      <span className="mt-1 block font-mono text-2xl font-semibold">{value}</span>
      {hint ? <span className="text-muted-foreground mt-1 block text-xs">{hint}</span> : null}
    </button>
  );
}

export function BatchSituationCards(props: { data: WorkspaceBatch }): JSX.Element {
  const { t } = useTranslation('workspace');
  const navigate = useNavigate();
  const { data } = props;

  // 规则4：无任何落库批次时整区空态 + 评测运营中心引导入口
  if (data.status === null) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{t('batch.title')}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col items-center gap-3 py-8 text-center">
          <p className="text-muted-foreground text-sm">{t('batch.emptyTitle')}</p>
          <p className="text-muted-foreground text-xs">{t('batch.emptyHint')}</p>
          <button
            type="button"
            onClick={() => {
              void navigate({ to: '/assessment' });
            }}
            className="text-primary inline-flex cursor-pointer items-center gap-0.5 text-sm hover:underline"
          >
            {t('batch.emptyGo')}
            <ChevronRight className="size-4" aria-hidden />
          </button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-normal text-muted-foreground">
          {t('batch.title')}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
          <span className="flex items-center gap-2">
            <span className="text-muted-foreground">{t('batch.statusLabel')}</span>
            <Badge variant={statusVariant(data.status)}>{t(`batch.status.${data.status}`)}</Badge>
          </span>
          {/* null 字段空态不渲染（specs §4.1.2 A 降级矩阵）；时间为分钟精度直出 */}
          {data.next_trigger_at !== null ? (
            <span className="text-muted-foreground">
              {t('batch.nextTrigger', { time: data.next_trigger_at })}
            </span>
          ) : null}
          {data.data_updated_at !== null ? (
            <span className="text-muted-foreground">
              {t('batch.dataUpdatedAt', { time: data.data_updated_at })}
            </span>
          ) : null}
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          {/* 跑批态势卡跳评测运营中心 AI 使用能力 tab（specs §4.1.3 / BR2） */}
          <ClickableCard
            label={t('batch.runCard')}
            value={t(`batch.status.${data.status}`)}
            hint={t('batch.runCardHint')}
            onClick={() => {
              void navigate({ to: '/assessment' });
            }}
          />
          {/* 告警卡跳评测运营中心，由批次列表与失败明细处置（specs §4.1.3 / BR3） */}
          <ClickableCard
            label={t('batch.alertCard')}
            value={data.alert_count ?? '-'}
            hint={t('batch.alertHint')}
            onClick={() => {
              void navigate({ to: '/assessment' });
            }}
          />
          {/* 逾期卡携 tab=ai_mgmt&status=expired 直达任务列表（specs §4.1.3 / BR4，已确认落 AI 管理能力 tab） */}
          <ClickableCard
            label={t('batch.overdueCard')}
            value={data.overdue_count ?? '-'}
            hint={t('batch.overdueHint')}
            onClick={() => {
              void navigate({
                to: '/assessment',
                search: { tab: 'ai_mgmt', status: 'expired' },
              });
            }}
          />
        </div>
      </CardContent>
    </Card>
  );
}
