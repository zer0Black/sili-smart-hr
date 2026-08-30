import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { AlertCircle, CheckCircle2, XCircle } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useHealthCheck, useSystemSummary } from '@/features/system/hooks';
import type { ComponentStatus, HealthResult } from '@/lib/contracts';

export const Route = createFileRoute('/_authenticated/system/status')({
  component: SystemStatusPage,
});

/**
 * 系统状态页（/system/status，挂 _authenticated 顶栏，系统配置下）。
 * 运行状态摘要页面加载即拉取；组件健康首入显空状态引导，点击立即测试才探测（BR24）。
 */
function SystemStatusPage() {
  const { t } = useTranslation('system');
  const summaryQ = useSystemSummary();
  const healthMut = useHealthCheck();
  // null 表示尚未触发过探测，组件健康区显示空状态引导（BR24）。
  const [health, setHealth] = useState<HealthResult | null>(null);

  // ISO 字符串按 YYYY-MM-DD HH:mm:ss 本地格式化，与 users 页统一走 dayjs（保留非法输入兜底）。
  const formatStartedAt = (raw: string): string =>
    dayjs(raw).isValid() ? dayjs(raw).format('YYYY-MM-DD HH:mm:ss') : raw;

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>{t('summaryTitle')}</CardTitle>
            <Button
              variant="outline"
              size="sm"
              disabled={summaryQ.isFetching}
              onClick={() => summaryQ.refetch()}
            >
              {t('refresh')}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-4 text-sm">
            <div className="flex flex-col gap-1">
              <dt className="text-muted-foreground">{t('initStatus')}</dt>
              <dd>
                {summaryQ.data?.initialized ? t('initialized') : t('notInitialized')}
              </dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="text-muted-foreground">{t('dbType')}</dt>
              <dd>{summaryQ.data?.db_type ?? '-'}</dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="text-muted-foreground">{t('version')}</dt>
              <dd>{summaryQ.data?.version ?? '-'}</dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="text-muted-foreground">{t('startedAt')}</dt>
              <dd>
                {summaryQ.data?.started_at ? formatStartedAt(summaryQ.data.started_at) : '-'}
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>{t('healthTitle')}</CardTitle>
            <Button
              size="sm"
              disabled={healthMut.isPending}
              // 仅触发 POST 探测，不传任何修改参数（BR19）；失败弹 toast，health 保持 null 允许重试。
              onClick={() =>
                healthMut.mutate(undefined, {
                  onSuccess: setHealth,
                  onError: () => toast.error(t('healthCheckFailed')),
                })
              }
            >
              {healthMut.isPending ? t('testing') : t('runTest')}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {health === null ? (
            <p className="text-muted-foreground text-sm">{t('healthEmptyHint')}</p>
          ) : (
            <ul className="flex flex-col gap-3 text-sm">
              {([
                { key: 'database', label: t('compDatabase'), value: health.database },
                { key: 'redis', label: t('compRedis'), value: health.redis },
                { key: 'llm', label: t('compLlm'), value: health.llm },
                { key: 'integration', label: t('compIntegration'), value: health.integration },
              ] as const).map(({ key, label, value }) => (
                <li key={key} className="flex items-center gap-2">
                  <StatusIcon value={value} />
                  <span>{label}</span>
                  <span className="text-muted-foreground">·</span>
                  {/* 状态值→文案映射：key 与 ComponentStatus 7 值一一对应 */}
                  <span>{t(`status_${value}`)}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

/** 状态图标：connected/reachable 绿勾，disconnected/unreachable 红叉，未配置档位黄警。 */
function StatusIcon({ value }: { value: ComponentStatus }) {
  if (value === 'connected' || value === 'reachable') {
    return <CheckCircle2 className="size-4 text-success" />;
  }
  if (value === 'disconnected' || value === 'unreachable') {
    return <XCircle className="size-4 text-destructive" />;
  }
  return <AlertCircle className="size-4 text-warning" />;
}
