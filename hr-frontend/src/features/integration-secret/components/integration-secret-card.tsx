// sili-smart-ap日志集成密钥卡片（specs §3.1 下区 / §4.2.2C / §4.2.3 / §4.2.4 规则4/5 / §4.2.5 / §4.4.4）。
// 单例卡片：掩码展示 + 配置状态标签 + 临时查看眼睛 + 更新/配置按钮 + 连通验证。
import { Eye, EyeOff } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { SecretUpdateDialog } from '@/features/integration-secret/components/secret-update-dialog';
import { useIntegrationSecret, useSecretDetail, useSecretTest } from '@/features/integration-secret/hooks';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

interface TestResult {
  ok: boolean;
  msg?: string;
}

/**
 * 集成密钥卡片。掩码 + 配置状态标签 + 临时查看 + 更新/配置 + 连通验证（specs §4.2.5）。
 * 明文仅当前页临时可见，刷新或切回隐藏时 removeQueries 清掉 detail 缓存（§4.4.4）。
 */
export function IntegrationSecretCard() {
  const { t } = useTranslation('integrationSecret');
  const secretQ = useIntegrationSecret();
  const configured = !!secretQ.data?.configured;
  const secretMasked = secretQ.data?.secret_masked;

  const [showSecret, setShowSecret] = useState(false);
  const detailQ = useSecretDetail(showSecret);

  const testMut = useSecretTest();
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

  // BR2 §4.2.3：眼睛图标就地切换明文/掩码；切回隐藏时清明文缓存。
  const onToggleReveal = () => {
    if (!configured) return;
    if (showSecret) {
      setShowSecret(false);
      void queryClient.removeQueries({ queryKey: ['integration-secret', 'detail'] });
    } else {
      setShowSecret(true);
    }
  };

  // BR4 §4.2.3 / §4.2.4 规则5：连通验证就地反馈成功/失败原因；1304 message 透传。
  const onTestConnection = () => {
    if (!configured || testMut.isPending) return;
    setTestResult(null);
    testMut.mutate(undefined, {
      onSuccess: (data) => {
        setTestResult({ ok: data.connected });
        if (data.connected) toast.success(t('card.testSuccess'));
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        if (code === ErrCode.IntegrationSecretTestFailed) {
          setTestResult({ ok: false, msg: err.message });
        } else if (code === ErrCode.IntegrationSecretNotConfigured) {
          toast.error(t('card.notConfiguredHint'));
        } else {
          toast.error(t('card.errGeneric'));
        }
      },
    });
  };

  const revealed = showSecret && detailQ.data?.secret;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('card.title')}</CardTitle>
        <CardDescription>{t('card.description')}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {/* 掩码行 + 配置状态标签 + 临时查看眼睛（§4.2.2C / §4.2.5） */}
        <div className="flex flex-wrap items-center gap-2">
          {revealed ? (
            <span className="font-mono text-warning text-sm break-all">{detailQ.data!.secret}</span>
          ) : configured ? (
            <span className="font-mono text-muted-foreground text-sm break-all">
              {secretMasked}
            </span>
          ) : (
            <span className="font-mono text-muted-foreground text-sm">
              {t('card.notConfigured')}
            </span>
          )}

          <button
            type="button"
            aria-label={showSecret ? t('card.hideLabel') : t('card.revealLabel')}
            onClick={onToggleReveal}
            disabled={!configured || detailQ.isFetching}
            className="text-muted-foreground hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            {showSecret ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>

          {/* 配置状态标签（§4.2.2C / §4.2.5）：已配置 success，未配置 muted（secondary） */}
          {configured ? (
            <Badge>{t('card.badgeConfigured')}</Badge>
          ) : (
            <Badge variant="secondary">{t('card.badgeNotConfigured')}</Badge>
          )}
        </div>

        {/* 操作区：更新/配置（§4.2.3） + 连通验证（§4.2.3 / §4.2.4 规则5） */}
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setDialogOpen(true)}>
            {configured ? t('card.update') : t('card.configure')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onTestConnection}
            disabled={!configured || testMut.isPending}
          >
            {testMut.isPending ? t('card.testing') : t('card.testConnection')}
          </Button>
        </div>

        {/* 连通验证失败原因就地提示（§4.2.4 规则6） */}
        {testResult && !testResult.ok && testResult.msg && (
          <p className="text-destructive text-sm">{testResult.msg}</p>
        )}

        <SecretUpdateDialog
          open={dialogOpen}
          configured={configured}
          version={secretQ.data?.version ?? 0}
          onOpenChange={setDialogOpen}
        />
      </CardContent>
    </Card>
  );
}
