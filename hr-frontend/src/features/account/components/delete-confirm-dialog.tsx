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
import { useDeleteAccount } from '@/features/account/hooks';
import type { AccountListItem } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';

export interface DeleteConfirmDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  account: AccountListItem | null;
}

/**
 * 删除账号二次确认。删除启用账号需非最后一个启用（后端返 1006 时提示）。
 * AlertDialogAction 默认点击即关闭，这里 preventDefault 阻断，改由 mutate 的 onSuccess 触发关闭，
 * 确保失败时弹窗保留、错误 toast 可见。
 */
export function DeleteConfirmDialog({
  open,
  onOpenChange,
  account,
}: DeleteConfirmDialogProps) {
  const { t } = useTranslation('account');
  const deleteMut = useDeleteAccount();

  const onError = (err: unknown) => {
    const code = err instanceof ApiError ? err.code : undefined;
    const msg =
      code === ErrCode.LastEnabledAccount
        ? t('users.errLastEnabled')
        : t('users.errGeneric');
    toast.error(msg);
  };

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('users.deleteTitle')}</AlertDialogTitle>
          <AlertDialogDescription>
            {account
              ? t('users.deleteConfirm', { username: account.username })
              : t('users.deleteConfirm', { username: '' })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleteMut.isPending}>
            {t('cancel', { ns: 'common' })}
          </AlertDialogCancel>
          <AlertDialogAction
            disabled={deleteMut.isPending}
            onClick={(e) => {
              if (!account) return;
              e.preventDefault();
              deleteMut.mutate(account.id, {
                onSuccess: () => {
                  toast.success(t('users.deleted'));
                  onOpenChange(false);
                },
                onError,
              });
            }}
          >
            {deleteMut.isPending
              ? t('users.submitting')
              : t('users.confirmDelete')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
