// 集成密钥更新弹窗（specs §4.5）。RHF + Zod + RSA-OAEP 加密 secret，仿 llm-form-dialog。
import { zodResolver } from '@hookform/resolvers/zod';
import { Eye, EyeOff } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { usePublicKey } from '@/features/account/hooks';
import { useUpdateIntegrationSecret } from '@/features/integration-secret/hooks';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ErrCode } from '@/lib/contracts';
import type { UpdateSecretPayload } from '@/lib/contracts';
import { encryptPasswordFresh } from '@/lib/crypto';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

export interface SecretUpdateDialogProps {
  open: boolean;
  configured: boolean;
  /** 乐观锁凭证，来自卡片当前 version，提交时原样回传。 */
  version: number;
  onOpenChange: (open: boolean) => void;
}

interface SecretFormValues {
  secret: string;
}

const emptyValues: SecretFormValues = { secret: '' };

/**
 * 集成密钥更新/首次配置弹窗。configured 决定标题/按钮/toast 文案分支（specs §4.5.1 / §4.5.3）。
 * 提交时实时拉一次性 RSA 公钥加密 secret，密文作 payload.secret、keyId 独立传递。
 */
export function SecretUpdateDialog({
  open,
  configured,
  version,
  onOpenChange,
}: SecretUpdateDialogProps) {
  const { t } = useTranslation('integrationSecret');
  const pubQ = usePublicKey();
  const updateMut = useUpdateIntegrationSecret();
  const isPending = updateMut.isPending;
  const pubReady = !pubQ.isError;

  const [showSecret, setShowSecret] = useState(false);

  // 单字段 schema：secret 恒必填，≤200（specs §4.5.2）。校验消息走 i18n。
  const schema = useMemo(() => {
    return z.object({
      secret: z
        .string()
        .min(1, t('secretUpdate.secretRequired'))
        .max(200, t('secretUpdate.secretMaxLength')),
    });
  }, [t]);

  const form = useForm<SecretFormValues>({
    resolver: zodResolver(schema),
    defaultValues: emptyValues,
  });

  const {
    register,
    handleSubmit,
    reset,
    setError,
    formState: { errors },
  } = form;

  // 弹窗打开时清空已填（specs §4.5「打开时 reset」）
  useEffect(() => {
    if (!open) return;
    reset(emptyValues);
    setShowSecret(false);
  }, [open, reset]);

  const onSubmit = async (values: SecretFormValues) => {
    let passwordCipher: string;
    let keyId: string;
    try {
      ({ passwordCipher, keyId } = await encryptPasswordFresh(values.secret));
    } catch {
      toast.error(t('secretUpdate.errGeneric'));
      void pubQ.refetch();
      return;
    }
    const payload: UpdateSecretPayload = {
      secret: passwordCipher,
      keyId,
      version,
    };
    updateMut.mutate(payload, {
      onSuccess: () => {
        toast.success(
          configured
            ? t('secretUpdate.updated')
            : t('secretUpdate.configured'),
        );
        onOpenChange(false);
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        // 并发更新冲突（1309）：密钥已被他人改过。废弃卡片缓存触发重取拿最新 version，
        // 关闭弹窗让用户重新打开拿到新 version，避免带旧 version 重试反复冲突。
        if (code === ErrCode.IntegrationSecretVersionConflict) {
          toast.error(t('secretUpdate.toastVersionConflict'));
          void queryClient.invalidateQueries({ queryKey: ['integration-secret'] });
          onOpenChange(false);
          return;
        }
        // 字段校验失败（1400）：内联报错保留已填，不关闭弹窗
        if (code === ErrCode.BadRequest) {
          setError('secret', { message: t('secretUpdate.secretRequired') });
          return;
        }
        toast.error(t('secretUpdate.errGeneric'));
      },
    });
  };

  const onCancel = () => {
    reset(emptyValues);
    setShowSecret(false);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-[480px]"
        onInteractOutside={(e) => e.preventDefault()}
        onPointerDownOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>
            {configured
              ? t('secretUpdate.titleUpdate')
              : t('secretUpdate.titleConfigure')}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="integration-secret-input">
              {t('secretUpdate.secretLabel')}
            </Label>
            <div className="relative">
              <Input
                id="integration-secret-input"
                type={showSecret ? 'text' : 'password'}
                autoComplete="off"
                placeholder={t('secretUpdate.secretPlaceholder')}
                {...register('secret')}
              />
              <button
                type="button"
                aria-label="toggle secret visibility"
                onClick={() => setShowSecret((v) => !v)}
                className="absolute inset-y-0 right-0 flex items-center px-3 text-muted-foreground hover:text-foreground"
                tabIndex={-1}
              >
                {showSecret ? (
                  <EyeOff className="size-4" />
                ) : (
                  <Eye className="size-4" />
                )}
              </button>
            </div>
            {errors.secret && (
              <p className="text-destructive text-sm">{errors.secret.message}</p>
            )}
            <p className="text-muted-foreground text-xs">
              {t('secretUpdate.hint')}
            </p>
          </div>

          {pubQ.isError && (
            <p className="text-destructive text-sm">
              {t('secretUpdate.publicKeyError')}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onCancel}
              disabled={isPending}
            >
              {t('secretUpdate.cancel')}
            </Button>
            <Button type="submit" disabled={isPending || !pubReady}>
              {isPending
                ? t('secretUpdate.submitting')
                : configured
                  ? t('secretUpdate.submitUpdate')
                  : t('secretUpdate.submitConfigure')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
