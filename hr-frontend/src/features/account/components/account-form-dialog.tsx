import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo } from 'react';
import { Controller, useForm } from 'react-hook-form';
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
import { Switch } from '@/components/ui/switch';
import { useCreateAccount, usePublicKey, useUpdateAccount } from '@/features/account/hooks';
import type { AccountListItem, UpdateAccountPayload } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { encryptPasswordFresh } from '@/lib/crypto';
import { ApiError } from '@/lib/http-client';
import { countRunes, isStrongPassword, USERNAME_PATTERN } from '@/lib/validation';

export interface AccountFormDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  mode: 'create' | 'edit';
  account?: AccountListItem;
}

interface AccountFormValues {
  username: string;
  name: string;
  password: string;
  enabled: boolean;
}

/**
 * 新增/编辑账号弹窗。新增录账号含初始密码，编辑改姓名、可选重置密码与启用状态（账号只读）。
 * 密码字段一律 RSA-OAEP 加密后提交；每次提交实时拉一次性公钥。
 */
export function AccountFormDialog({
  open,
  onOpenChange,
  mode,
  account,
}: AccountFormDialogProps) {
  const { t } = useTranslation('account');
  const pubQ = usePublicKey();
  const createMut = useCreateAccount();
  const updateMut = useUpdateAccount();
  const isPending = createMut.isPending || updateMut.isPending;
  const pubReady = !pubQ.isError;

  // schema 按 mode 分支：create 走完整校验；edit 账号只读、密码空串表示不改。
  const schema = useMemo(() => {
    return z.object({
      username: z
        .string()
        .min(mode === 'create' ? 3 : 1, t('users.usernameMinLength'))
        .max(30, t('users.usernameMaxLength'))
        .refine(
          (v) => mode !== 'create' || USERNAME_PATTERN.test(v),
          t('users.usernamePattern'),
        ),
      name: z
        .string()
        .min(1, t('users.nameRequired'))
        .refine((v) => countRunes(v) <= 20, t('users.nameMaxLength')),
      password: z.string().refine((v) => {
        if (mode === 'edit' && v === '') return true;
        return isStrongPassword(v);
      }, t('users.passwordWeak')),
      enabled: z.boolean(),
    });
  }, [mode, t]);

  const form = useForm<AccountFormValues>({
    resolver: zodResolver(schema),
    defaultValues:
      mode === 'edit' && account
        ? {
            username: account.username,
            name: account.name,
            password: '',
            enabled: account.enabled,
          }
        : { username: '', name: '', password: '', enabled: true },
  });

  const {
    register,
    handleSubmit,
    control,
    reset,
    formState: { errors },
  } = form;

  // 弹窗打开时按模式回填：edit 取 account，create 清空。
  useEffect(() => {
    if (!open) return;
    if (mode === 'edit' && account) {
      reset({
        username: account.username,
        name: account.name,
        password: '',
        enabled: account.enabled,
      });
    } else {
      reset({ username: '', name: '', password: '', enabled: true });
    }
  }, [open, mode, account, reset]);

  const onError = (err: unknown) => {
    const code = err instanceof ApiError ? err.code : undefined;
    const msg =
      code === ErrCode.UsernameExists
        ? t('users.errUsernameExists')
        : code === ErrCode.LastEnabledAccount
          ? t('users.errLastEnabled')
          : code === ErrCode.PasswordInvalid
            ? t('users.errPasswordInvalid')
            : code === ErrCode.BadRequest
              ? t('users.errBadRequest')
              : t('users.errGeneric');
    toast.error(msg);
  };

  const onSubmit = async (values: AccountFormValues) => {
    if (mode === 'create') {
      let passwordCipher: string;
      let keyId: string;
      try {
        ({ passwordCipher, keyId } = await encryptPasswordFresh(values.password));
      } catch {
        toast.error(t('users.errGeneric'));
        void pubQ.refetch();
        return;
      }
      createMut.mutate(
        {
          username: values.username.trim(),
          name: values.name,
          passwordCipher,
          keyId,
          enabled: values.enabled,
        },
        {
          onSuccess: () => {
            toast.success(t('users.created'));
            onOpenChange(false);
          },
          onError,
        },
      );
      return;
    }
    // edit
    if (!account) return;
    const payload: UpdateAccountPayload = {
      id: account.id,
      name: values.name,
      enabled: values.enabled,
    };
    if (values.password !== '') {
      try {
        const fresh = await encryptPasswordFresh(values.password);
        payload.passwordCipher = fresh.passwordCipher;
        payload.keyId = fresh.keyId;
      } catch {
        toast.error(t('users.errGeneric'));
        void pubQ.refetch();
        return;
      }
    }
    updateMut.mutate(payload, {
      onSuccess: () => {
        toast.success(t('users.updated'));
        onOpenChange(false);
      },
      onError,
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {mode === 'create' ? t('users.createTitle') : t('users.editTitle')}
          </DialogTitle>
          <DialogDescription>
            {mode === 'create' ? t('users.createSubtitle') : t('users.editSubtitle')}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="account-username">{t('users.username')}</Label>
            <Input
              id="account-username"
              autoComplete="off"
              placeholder={t('users.usernamePlaceholder')}
              disabled={mode === 'edit'}
              {...register('username')}
            />
            {errors.username && (
              <p className="text-destructive text-sm">{errors.username.message}</p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="account-name">{t('users.name')}</Label>
            <Input
              id="account-name"
              autoComplete="off"
              placeholder={t('users.namePlaceholder')}
              {...register('name')}
            />
            {errors.name && (
              <p className="text-destructive text-sm">{errors.name.message}</p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="account-password">{t('users.password')}</Label>
            <Input
              id="account-password"
              type="password"
              autoComplete="new-password"
              placeholder={
                mode === 'edit'
                  ? t('users.passwordEditHint')
                  : t('users.passwordPlaceholder')
              }
              {...register('password')}
            />
            {errors.password && (
              <p className="text-destructive text-sm">{errors.password.message}</p>
            )}
          </div>
          <div className="flex items-center justify-between rounded-md border p-3">
            <div className="flex flex-col gap-0.5">
              <Label htmlFor="account-enabled" className="cursor-pointer">
                {t('users.enabled')}
              </Label>
              <span className="text-muted-foreground text-xs">
                {t('users.enabledHint')}
              </span>
            </div>
            <Controller
              control={control}
              name="enabled"
              render={({ field }) => (
                <Switch
                  id="account-enabled"
                  checked={field.value}
                  onCheckedChange={field.onChange}
                />
              )}
            />
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
              disabled={isPending}
            >
              {t('cancel', { ns: 'common' })}
            </Button>
            <Button type="submit" disabled={isPending || !pubReady}>
              {isPending
                ? t('users.submitting')
                : mode === 'create'
                  ? t('users.confirmCreate')
                  : t('users.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
