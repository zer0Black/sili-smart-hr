// 新增/编辑模型弹窗（specs §4.3）。RHF + Zod + RSA-OAEP 加密 api_key，仿 account-form-dialog。
import { zodResolver } from '@hookform/resolvers/zod';
import { Eye, EyeOff } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { usePublicKey } from '@/features/account/hooks';
import {
  useCreateLLMConfig,
  useUpdateLLMConfig,
} from '@/features/llm-config/hooks';
import type { LLMFormValues, LLMProvider } from '@/features/llm-config/types';
import { PROVIDER_OPTIONS } from '@/features/llm-config/types';
import type { CreateLLMPayload, LLMConfigItem, UpdateLLMPayload } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { encryptPasswordFresh } from '@/lib/crypto';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

export interface LLMFormDialogProps {
  open: boolean;
  mode: 'create' | 'edit';
  onOpenChange: (open: boolean) => void;
  llm?: LLMConfigItem | null;
  // edit 成功后通知父组件清理 revealedKeys，避免列表重拉后渲染已被替换的旧明文。
  onUpdateSuccess?: () => void;
}

const emptyValues: LLMFormValues = {
  name: '',
  provider: 'deepseek',
  model_id: '',
  api_url: '',
  api_key: '',
};

/**
 * 新增/编辑模型弹窗。create 模式 api_key 必填，edit 模式留空保持原 Key 不变（specs §4.3.4 规则2）。
 * api_key 非空时实时拉一次性公钥 RSA-OAEP 加密，密文作 api_key、keyId 独立传递；
 * api_key 为空（仅 edit）则 payload 不含 api_key/keyId。
 */
export function LLMFormDialog({ open, mode, onOpenChange, llm, onUpdateSuccess }: LLMFormDialogProps) {
  const { t } = useTranslation('llmConfig');
  const pubQ = usePublicKey();
  const createMut = useCreateLLMConfig();
  const updateMut = useUpdateLLMConfig();
  const isPending = createMut.isPending || updateMut.isPending;
  const pubReady = !pubQ.isError;

  const [showApiKey, setShowApiKey] = useState(false);

  // schema 按 mode 分支：create 走 api_key 必填；edit 留空表示不改（specs §4.3.2 / §4.3.4 规则2）。
  const schema = useMemo(() => {
    return z.object({
      name: z
        .string()
        .min(1, t('form.nameRequired'))
        .max(50, t('form.nameMaxLength')),
      provider: z
        .enum(['deepseek', 'openai', 'zhipu', 'anthropic'], {
          message: t('form.providerInvalid'),
        }),
      model_id: z
        .string()
        .min(1, t('form.modelIdRequired'))
        .max(100, t('form.modelIdMaxLength')),
      api_url: z
        .string()
        .max(500, t('form.apiUrlMaxLength'))
        .refine(
          (v) => v.trim() === '' || /^https?:\/\/.+/i.test(v.trim()),
          t('form.apiUrlInvalid'),
        ),
      api_key: z
        .string()
        .max(200, t('form.apiKeyMaxLength'))
        .refine(
          (v) => mode !== 'create' || v.length > 0,
          t('form.apiKeyRequired'),
        ),
    });
  }, [mode, t]);

  const form = useForm<LLMFormValues>({
    resolver: zodResolver(schema),
    defaultValues: emptyValues,
  });

  const {
    register,
    handleSubmit,
    control,
    reset,
    setError,
    formState: { errors },
  } = form;

  // 弹窗打开时按模式回填（specs §4.3.3）：edit 取 llm，create 清空。
  useEffect(() => {
    if (!open) return;
    if (mode === 'edit' && llm) {
      reset({
        name: llm.name,
        provider: llm.provider,
        model_id: llm.model_id,
        api_url: llm.api_url ?? '',
        api_key: '',
      });
    } else {
      reset(emptyValues);
    }
    setShowApiKey(false);
  }, [open, mode, llm, reset]);

  const onError = (err: unknown) => {
    const code = err instanceof ApiError ? err.code : undefined;
    if (code === ErrCode.BadRequest) {
      toast.error(t('form.errBadRequest'));
      return;
    }
    toast.error(t('form.errGeneric'));
  };

  const onSubmit = async (values: LLMFormValues) => {
    const trimmedUrl = values.api_url.trim();
    if (mode === 'create') {
      let passwordCipher: string;
      let keyId: string;
      try {
        ({ passwordCipher, keyId } = await encryptPasswordFresh(values.api_key));
      } catch {
        toast.error(t('form.errGeneric'));
        void pubQ.refetch();
        return;
      }
      const payload: CreateLLMPayload = {
        name: values.name.trim(),
        provider: values.provider,
        model_id: values.model_id.trim(),
        api_key: passwordCipher,
        keyId,
      };
      if (trimmedUrl !== '') payload.api_url = trimmedUrl;
      createMut.mutate(payload, {
        onSuccess: () => {
          toast.success(t('form.created'));
          onOpenChange(false);
        },
        onError,
      });
      return;
    }
    // edit
    if (!llm) return;
    const payload: UpdateLLMPayload = {
      id: llm.id,
      version: llm.version,
      name: values.name.trim(),
      provider: values.provider,
      model_id: values.model_id.trim(),
    };
    if (trimmedUrl !== '') payload.api_url = trimmedUrl;
    // api_key 非空才加密提交，留空保持原 Key 不变（specs §4.3.4 规则2）
    if (values.api_key !== '') {
      try {
        const fresh = await encryptPasswordFresh(values.api_key);
        payload.api_key = fresh.passwordCipher;
        payload.keyId = fresh.keyId;
      } catch {
        toast.error(t('form.errGeneric'));
        void pubQ.refetch();
        return;
      }
    }
    updateMut.mutate(payload, {
      onSuccess: () => {
        toast.success(t('form.updated'));
        onOpenChange(false);
        onUpdateSuccess?.();
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        // 并发更新冲突（1308）：配置已被他人改过。废弃列表缓存触发重取拿最新 version，
        // 关闭弹窗让用户重新打开编辑（重新打开从最新列表取新 version），避免带旧 version 重试反复冲突。
        if (code === ErrCode.LLMConfigVersionConflict) {
          toast.error(t('form.toastVersionConflict'));
          void queryClient.invalidateQueries({ queryKey: ['llm-configs'] });
          onOpenChange(false);
          return;
        }
        // 字段校验失败（1400）：内联报错保留已填，不关闭弹窗（specs §4.2.4 规则6）
        if (code === ErrCode.BadRequest) {
          const msg = err instanceof ApiError ? err.message : '';
          // 后端返回 "field is invalid" / "field too long" / "field is required" / "field decrypt failed"
          // 用精确前缀匹配映射字段，避免 'url'/'key' 子串误命中。
          if (msg.startsWith('name ')) {
            setError('name', { message: t('form.nameRequired') });
          } else if (msg.startsWith('provider ')) {
            setError('provider', { message: t('form.providerInvalid') });
          } else if (msg.startsWith('model_id ')) {
            setError('model_id', { message: t('form.modelIdRequired') });
          } else if (msg.startsWith('api_url ')) {
            setError('api_url', { message: t('form.apiUrlInvalid') });
          } else if (msg.startsWith('api_key ')) {
            setError('api_key', { message: t('form.apiKeyRequired') });
          } else {
            toast.error(t('form.errBadRequest'));
          }
          return;
        }
        onError(err);
      },
    });
  };

  const onCancel = () => {
    reset(emptyValues);
    setShowApiKey(false);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-[560px]"
        onInteractOutside={(e) => e.preventDefault()}
        onPointerDownOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>
            {mode === 'create' ? t('form.createTitle') : t('form.editTitle')}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          {/* 模型名称 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="llm-name">{t('form.name')}</Label>
            <Input
              id="llm-name"
              autoComplete="off"
              placeholder={t('form.namePlaceholder')}
              {...register('name')}
            />
            {errors.name && (
              <p className="text-destructive text-sm">{errors.name.message}</p>
            )}
          </div>

          {/* 服务商 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="llm-provider">{t('form.provider')}</Label>
            <Controller
              control={control}
              name="provider"
              render={({ field }) => (
                <Select
                  value={field.value}
                  onValueChange={(v) => field.onChange(v as LLMProvider)}
                >
                  <SelectTrigger id="llm-provider" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PROVIDER_OPTIONS.map((opt) => (
                      <SelectItem key={opt.value} value={opt.value}>
                        {t(`form.provider${opt.value.charAt(0).toUpperCase()}${opt.value.slice(1)}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
            <p className="text-muted-foreground text-xs">
              {t('form.providerHint')}
            </p>
            {errors.provider && (
              <p className="text-destructive text-sm">{errors.provider.message}</p>
            )}
          </div>

          {/* 模型 ID */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="llm-model-id">{t('form.modelId')}</Label>
            <Input
              id="llm-model-id"
              autoComplete="off"
              placeholder={t('form.modelIdPlaceholder')}
              {...register('model_id')}
            />
            {errors.model_id && (
              <p className="text-destructive text-sm">{errors.model_id.message}</p>
            )}
          </div>

          {/* API 地址 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="llm-api-url">{t('form.apiUrl')}</Label>
            <Input
              id="llm-api-url"
              autoComplete="off"
              placeholder={t('form.apiUrlPlaceholder')}
              {...register('api_url')}
            />
            <p className="text-muted-foreground text-xs">
              {t('form.apiUrlHint')}
            </p>
            {errors.api_url && (
              <p className="text-destructive text-sm">{errors.api_url.message}</p>
            )}
          </div>

          {/* API Key */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="llm-api-key">{t('form.apiKey')}</Label>
            <div className="relative">
              <Input
                id="llm-api-key"
                type={showApiKey ? 'text' : 'password'}
                autoComplete="off"
                placeholder={t('form.apiKeyPlaceholderCreate')}
                {...register('api_key')}
              />
              <button
                type="button"
                aria-label="toggle api key visibility"
                onClick={() => setShowApiKey((v) => !v)}
                className="absolute inset-y-0 right-0 flex items-center px-3 text-muted-foreground hover:text-foreground"
                tabIndex={-1}
              >
                {showApiKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
              </button>
            </div>
            <p className="text-muted-foreground text-xs">
              {mode === 'edit'
                ? t('form.apiKeyHintEdit')
                : t('form.apiKeyHintCreate')}
            </p>
            {errors.api_key && (
              <p className="text-destructive text-sm">{errors.api_key.message}</p>
            )}
          </div>

          {pubQ.isError && (
            <p className="text-destructive text-sm">
              {t('form.publicKeyError')}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onCancel}
              disabled={isPending}
            >
              {t('form.cancel')}
            </Button>
            <Button type="submit" disabled={isPending || !pubReady}>
              {isPending
                ? t('form.submitting')
                : mode === 'create'
                  ? t('form.submitCreate')
                  : t('form.submitEdit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
