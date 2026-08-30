// 查看模型弹窗（specs §4.4 / §4.2.3）。只读展示详情含 api_key 明文，关闭即丢弃不缓存。
import { Copy, Pencil } from 'lucide-react';
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useLLMDetail } from '@/features/llm-config/hooks';
import type { LLMConfigDetail } from '@/lib/contracts';
import { queryClient } from '@/lib/query-client';

export interface LLMViewDialogProps {
  open: boolean;
  llmId: string | null;
  onOpenChange: (open: boolean) => void;
  /** 跳转编辑：关闭查看弹窗后由父组件打开当前模型的编辑弹窗。 */
  onEdit: (llm: LLMConfigDetail) => void;
}

export function LLMViewDialog({ open, llmId, onOpenChange, onEdit }: LLMViewDialogProps) {
  const { t } = useTranslation('llmConfig');
  const detailQ = useLLMDetail(open ? llmId : null);

  // 明文不缓存：关闭查看弹窗后清掉 ['llm-configs','detail'] 前缀，避免明文长留内存（specs §4.4.3）。
  useEffect(() => {
    if (open) return;
    if (llmId === null) return;
    void queryClient.removeQueries({ queryKey: ['llm-configs', 'detail'] });
  }, [open, llmId]);

  const detail = detailQ.data;
  const title = detail
    ? t('view.titleWithName', { name: detail.name })
    : t('view.title');

  const handleCopy = async () => {
    if (!detail) return;
    try {
      await navigator.clipboard.writeText(detail.api_key);
      toast.success(t('view.copySuccess'));
    } catch {
      toast.error(t('view.copyFailed'));
    }
  };

  const handleEdit = () => {
    if (!detail) return;
    // BR2 §4.4.3：先关闭查看弹窗，再回调父组件打开编辑弹窗。
    onOpenChange(false);
    onEdit(detail);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-[600px]"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>

        {detailQ.isLoading && (
          <p className="text-muted-foreground text-sm">{t('view.loading')}</p>
        )}

        {detail && (
          <div className="flex flex-col gap-4">
            {/* 基本信息 */}
            <dl className="grid grid-cols-[120px_1fr] gap-x-4 gap-y-3 text-sm">
              <dt className="text-muted-foreground">{t('view.name')}</dt>
              <dd className="font-medium">{detail.name}</dd>

              <dt className="text-muted-foreground">{t('view.provider')}</dt>
              <dd>{t(`form.provider${detail.provider.charAt(0).toUpperCase()}${detail.provider.slice(1)}`)}</dd>

              <dt className="text-muted-foreground">{t('view.modelId')}</dt>
              <dd className="font-mono">{detail.model_id}</dd>

              <dt className="text-muted-foreground">{t('view.apiUrl')}</dt>
              <dd className="font-mono break-all">
                {detail.api_url || t('view.empty')}
              </dd>

              <dt className="text-muted-foreground">{t('view.status')}</dt>
              <dd>
                <Badge variant={detail.enabled ? 'default' : 'secondary'}>
                  {detail.enabled ? t('view.enabled') : t('view.disabled')}
                </Badge>
              </dd>
            </dl>

            {/* API Key 独立信息块：等宽字体，附模型 ID 与复制按钮（specs §4.4.3） */}
            <div
              className="rounded-md border bg-muted/40 p-3"
              data-testid="llm-apikey-block"
            >
              <div className="flex items-center justify-between gap-2">
                <div className="flex flex-col gap-1">
                  <span className="font-mono text-muted-foreground text-xs uppercase tracking-wide">
                    {t('view.apiKey')}
                  </span>
                  <span className="text-muted-foreground text-xs">
                    {detail.model_id}
                  </span>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={handleCopy}
                  aria-label={t('view.copy')}
                >
                  <Copy className="size-4" />
                  {t('view.copy')}
                </Button>
              </div>
              <code className="mt-2 block font-mono text-sm break-all">
                {detail.api_key}
              </code>
            </div>
          </div>
        )}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={!detail}
          >
            {t('view.close')}
          </Button>
          <Button
            type="button"
            onClick={handleEdit}
            disabled={!detail}
          >
            <Pencil className="size-4" />
            {t('view.edit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
