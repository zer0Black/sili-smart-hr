// 作答链接弹窗（specs P2_TST_001 §4.3）：任务元信息 + 链接全文与复制 + 状态提示。
// 打开即拉、不轮询（§4.3.5 快照口径）；非 valid 置灰复制并提示处置方式（§4.3.4 规则1）。
// 重发入口在任务行操作列（§4.3.4 约束说明）：父层以 linkData 直接喂重发响应展示
// 新链接（§4.1.3），本组件完全受控只渲染 props。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';
import { toast } from 'sonner';
import { Copy } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useTestTaskLink } from '@/features/assessment/test-task-hooks';
import { linkStatusVariant, type TestTaskLinkInfo } from '@/features/assessment/test-task-types';
import { cn } from '@/lib/utils';

export interface AnswerLinkDialogProps {
  open: boolean;
  taskId: string | null;
  onClose: () => void;
  /** 外部注入的链接数据：行内重发 onSuccess 以响应直接喂弹窗展示新链接（§4.1.3）。 */
  linkData?: TestTaskLinkInfo | null;
}

export function AnswerLinkDialog({
  open,
  taskId,
  onClose,
  linkData = null,
}: AnswerLinkDialogProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const linkQ = useTestTaskLink(open ? taskId : null);
  const info = linkData ?? linkQ.data;

  async function handleCopy() {
    if (!info || info.link_status !== 'valid') return;
    try {
      await navigator.clipboard.writeText(info.answer_url);
      toast.success(t('testTask.linkDialog.copySuccess'));
    } catch {
      toast.error(t('testTask.linkDialog.copyFailed'));
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('testTask.linkDialog.title')}</DialogTitle>
        </DialogHeader>

        {linkQ.isLoading && !info ? (
          <p className="text-muted-foreground text-sm">{t('testTask.linkDialog.loading')}</p>
        ) : linkQ.isError && !info ? (
          // 失败态弹窗内重试，不关闭弹窗（§4.3.5，与 F6 失败明细弹窗同口径）
          <div className="flex flex-col items-center gap-2 py-6">
            <span className="text-muted-foreground text-sm">
              {t('testTask.linkDialog.loadError')}
            </span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void linkQ.refetch()}
            >
              {t('testTask.linkDialog.retry')}
            </Button>
          </div>
        ) : info ? (
          <>
            {/* 顶部描述列表：任务号（等宽）/ 对象 / 评测类型（§4.3.5） */}
            <dl className="grid grid-cols-[100px_1fr] gap-x-4 gap-y-2 text-sm">
              <dt className="text-muted-foreground">{t('testTask.linkDialog.taskNo')}</dt>
              <dd className="font-mono font-medium">{info.task_no}</dd>
              <dt className="text-muted-foreground">{t('testTask.linkDialog.staff')}</dt>
              <dd>{info.staff_name}</dd>
              <dt className="text-muted-foreground">{t('testTask.linkDialog.testType')}</dt>
              <dd>
                {info.test_type === 'enneagram'
                  ? t('create.typeEnneagram')
                  : t('create.typeAiMgmt')}
              </dd>
              <dt className="text-muted-foreground">{t('testTask.linkDialog.generatedAt')}</dt>
              <dd>{info.generated_at}</dd>
            </dl>

            {/* 链接框：全文 + 复制按钮；非 valid 置灰禁复制（§4.3.4 规则1） */}
            <div className="rounded-md border bg-muted/40 p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-muted-foreground font-mono text-xs uppercase tracking-wide">
                  {t('testTask.linkDialog.linkLabel')}
                </span>
                <div className="flex items-center gap-2">
                  <Badge variant={linkStatusVariant(info.link_status)}>
                    {t(`testTask.table.linkStatus.${info.link_status}`)}
                  </Badge>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleCopy()}
                    disabled={info.link_status !== 'valid'}
                  >
                    <Copy className="size-4" />
                    {t('testTask.linkDialog.copy')}
                  </Button>
                </div>
              </div>
              <code
                className={cn(
                  'mt-2 block font-mono text-sm break-all',
                  info.link_status !== 'valid' && 'text-muted-foreground',
                )}
              >
                {info.answer_url}
              </code>
              {info.link_status === 'used' && (
                <p className="text-muted-foreground mt-2 text-xs">
                  {t('testTask.linkDialog.usedHint')}
                </p>
              )}
              {info.link_status === 'invalid' && (
                <p className="text-muted-foreground mt-2 text-xs">
                  {t('testTask.linkDialog.invalidHint')}
                </p>
              )}
            </div>

            {/* 底部信息条（§4.3.5） */}
            <p className="text-muted-foreground rounded-md border-l-2 pl-2 text-xs">
              {t('testTask.linkDialog.footerHint')}
            </p>
          </>
        ) : null}

        <DialogFooter>
          {/* 页脚复制链接（§4.3.3 第二入口）与关闭并列 */}
          <Button
            type="button"
            variant="outline"
            onClick={() => void handleCopy()}
            disabled={!info || info.link_status !== 'valid'}
          >
            <Copy className="size-4" />
            {t('testTask.linkDialog.copyLink')}
          </Button>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('testTask.linkDialog.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
