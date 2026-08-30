// 删除模型二次确认（specs §4.2.5）。AlertDialog 二次确认，确认按钮 destructive variant。
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { useDeleteLLMConfig, useLLMConfigs } from '@/features/llm-config/hooks';
import type { LLMConfigItem, LLMDeleteResult } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';

export interface LLMDeleteDialogProps {
  open: boolean;
  llm: LLMConfigItem | null;
  onOpenChange: (open: boolean) => void;
}

export function LLMDeleteDialog({ open, llm, onOpenChange }: LLMDeleteDialogProps) {
  const { t } = useTranslation('llmConfig');
  const deleteMut = useDeleteLLMConfig();
  // 列表数据用于把 transferred_enabled_id 反查转启模型名（specs §4.2.5）。
  const listQ = useLLMConfigs('');

  const findTransferName = (result: LLMDeleteResult): string | null => {
    if (!result.transferred_enabled_id) return null;
    const found = listQ.data?.find((it) => it.id === result.transferred_enabled_id);
    return found?.name ?? null;
  };

  const onConfirm = (e: React.MouseEvent<HTMLButtonElement>) => {
    // BR3 §4.2.5：阻断 AlertDialog 默认关闭，由 mutate 的 onSuccess 触发关闭（失败弹窗保留）。
    e.preventDefault();
    if (!llm) return;
    deleteMut.mutate(llm.id, {
      onSuccess: (result) => {
        // BR4 §4.2.4 规则3：删除启用态模型自动转启首个剩余，前端提示转启结果。
        const name = findTransferName(result);
        if (result.transferred_enabled_id) {
          if (name) {
            toast.success(t('delete.transferredEnabled', { name }));
          } else {
            toast.success(t('delete.transferredEnabledUnknown'));
          }
        } else {
          toast.success(t('delete.deleted'));
        }
        onOpenChange(false);
      },
      onError: (err) => {
        // BR7 §4.2.4 规则3：删除仅剩一个时 1302 提示「至少保留一个模型」。
        const code = err instanceof ApiError ? err.code : undefined;
        if (code === ErrCode.LastLLMConfig) {
          toast.error(t('delete.errLastModel'));
          return;
        }
        toast.error(t('delete.errGeneric'));
      },
    });
  };

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('delete.title')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('delete.confirm', { name: llm?.name ?? '' })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleteMut.isPending}>
            {t('delete.cancel')}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={onConfirm}
            disabled={deleteMut.isPending}
          >
            {deleteMut.isPending ? t('delete.deleting') : t('delete.confirmDelete')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
