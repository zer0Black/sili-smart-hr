// 保存配置/规则通用二次确认弹窗。用 Dialog（确认操作而非危险操作，比 AlertDialog 轻）。
// 文案默认按 specs §4.1.4 规则1（下次评估生效），title/desc 可覆盖。
import { useTranslation } from 'react-i18next';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';

export interface SaveConfirmDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onConfirm: () => void;
  title?: string;
  desc?: string;
  submitting?: boolean;
}

/**
 * 保存二次确认弹窗。T3 leaf-config-form 与 T4 activity-rule-panel 的占位 AlertDialog
 * 替换为本组件，调用方在 RHF onValid 里 setConfirmOpen(true)，确认后 onConfirm 真正提交。
 */
export function SaveConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
  title,
  desc,
  submitting = false,
}: SaveConfirmDialogProps) {
  const { t } = useTranslation('dimension');
  const resolvedTitle = title ?? t('saveConfirm.defaultTitle');
  const resolvedDesc = desc ?? t('saveConfirm.defaultDesc');
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[420px]">
        <DialogHeader>
          <DialogTitle>{resolvedTitle}</DialogTitle>
          <DialogDescription>{resolvedDesc}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            {t('saveConfirm.cancel')}
          </Button>
          <Button
            type="button"
            disabled={submitting}
            onClick={() => {
              onConfirm();
            }}
          >
            {submitting ? t('saveConfirm.submitting') : t('saveConfirm.confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
