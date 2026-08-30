import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { usePublicKey, useResetPassword } from '@/features/account/hooks';
import type { AccountListItem } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { encryptPasswordFresh } from '@/lib/crypto';
import { ApiError } from '@/lib/http-client';
import { isStrongPassword } from '@/lib/validation';

export interface ResetPasswordDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  account: AccountListItem | null;
}

interface ResetPasswordValues {
  password: string;
}

/** 重置密码弹窗。只提交 passwordCipher/keyId，后端不改启用状态。每次提交实时拉一次性公钥。 */
export function ResetPasswordDialog({
  open,
  onOpenChange,
  account,
}: ResetPasswordDialogProps) {
  const { t } = useTranslation('account');
  const pubQ = usePublicKey();
  const resetMut = useResetPassword();
  const pubReady = !pubQ.isError;

  const schema = useMemo(() => {
    return z.object({
      password: z.string().refine((v) => isStrongPassword(v), t('users.passwordWeak')),
    });
  }, [t]);

  const form = useForm<ResetPasswordValues>({
    resolver: zodResolver(schema),
    defaultValues: { password: '' },
  });

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = form;

  // 打开时清空密码字段，避免上一次输入残留。
  useEffect(() => {
    if (open) {
      reset({ password: '' });
    }
  }, [open, reset]);

  const onError = (err: unknown) => {
    const code = err instanceof ApiError ? err.code : undefined;
    // 1007 密码不合规；1400 解密失败/keyId 过期；其余通用错误。
    const msg =
      code === ErrCode.PasswordInvalid
        ? t('users.errPasswordInvalid')
        : code === ErrCode.BadRequest
          ? t('users.errBadRequest')
          : t('users.errGeneric');
    toast.error(msg);
  };

  const onSubmit = async (values: ResetPasswordValues) => {
    if (!account) return;
    let passwordCipher: string;
    let keyId: string;
    try {
      ({ passwordCipher, keyId } = await encryptPasswordFresh(values.password));
    } catch {
      toast.error(t('users.errGeneric'));
      void pubQ.refetch();
      return;
    }
    resetMut.mutate(
      {
        id: account.id,
        passwordCipher,
        keyId,
      },
      {
        onSuccess: () => {
          toast.success(t('users.passwordReset'));
          onOpenChange(false);
        },
        onError,
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('users.resetTitle')}</DialogTitle>
          <DialogDescription>{t('users.resetSubtitle')}</DialogDescription>
        </DialogHeader>
        {account && (
          <p className="rounded-md border p-3 text-sm">
            {t('users.resetTarget', {
              username: account.username,
              name: account.name,
            })}
          </p>
        )}
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="reset-password">{t('users.newPassword')}</Label>
            <Input
              id="reset-password"
              type="password"
              autoComplete="new-password"
              placeholder={t('users.passwordPlaceholder')}
              {...register('password')}
            />
            {errors.password && (
              <p className="text-destructive text-sm">{errors.password.message}</p>
            )}
          </div>
          {pubQ.isError && (
            <p className="text-destructive text-sm">
              {t('publicKeyError', { ns: 'auth' })}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={resetMut.isPending}
            >
              {t('cancel', { ns: 'common' })}
            </Button>
            <Button type="submit" disabled={resetMut.isPending || !pubReady}>
              {resetMut.isPending
                ? t('users.submitting')
                : t('users.confirmReset')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
