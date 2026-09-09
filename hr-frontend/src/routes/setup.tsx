import { zodResolver } from '@hookform/resolvers/zod';
import { AlertCircle, CheckCircle2, XCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { AuthSplitLayout } from '@/components/auth-split-layout';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { usePublicKey } from '@/features/account/hooks';
import { useInitialize, useSetupStatus } from '@/features/system/hooks';
import type { SetupFormValues } from '@/features/system/types';
import { encryptPasswordFresh } from '@/lib/crypto';
import { ApiError } from '@/lib/http-client';
import { ErrCode } from '@/lib/contracts';
import { countRunes, isStrongPassword, USERNAME_PATTERN } from '@/lib/validation';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/setup')({
  component: SetupPage,
});

function SetupPage() {
  const { t } = useTranslation('setup');
  const navigate = useNavigate();
  const statusQ = useSetupStatus();
  const pubQ = usePublicKey();
  const initMut = useInitialize();
  const [done, setDone] = useState(false);

  // done 置 true 后 2 秒自动跳登录；用 effect 管理定时器，卸载或 done 变化时清理，避免手动跳转后的 stale 触发。
  useEffect(() => {
    if (!done) return;
    const timer = setTimeout(() => void navigate({ to: '/login' }), 2000);
    return () => clearTimeout(timer);
  }, [done, navigate]);

  // zod schema：校验口径对齐 account（username 3-30 字母数字下划线、name ≤20 rune、password ≥8 且字母+数字）。
  const schema = z
    .object({
      username: z
        .string()
        .min(3, t('usernameInvalid'))
        .max(30, t('usernameInvalid'))
        .refine((v) => USERNAME_PATTERN.test(v), t('usernameInvalid')),
      name: z
        .string()
        .min(1, t('nameRequired'))
        .refine((s) => countRunes(s) <= 20, t('nameTooLong')),
      password: z
        .string()
        .refine((s) => isStrongPassword(s), t('passwordWeak')),
      confirmPassword: z.string(),
    })
    .refine((d) => d.password === d.confirmPassword, {
      path: ['confirmPassword'],
      message: t('passwordMismatch'),
    });

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<SetupFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', name: '', password: '', confirmPassword: '' },
  });

  const onSubmit = async (values: SetupFormValues) => {
    // keyId 严格一次性，每次提交实时拉新公钥加密。
    let passwordCipher: string;
    let keyId: string;
    try {
      ({ passwordCipher, keyId } = await encryptPasswordFresh(values.password));
    } catch {
      toast.error(t('errGeneric'));
      void pubQ.refetch();
      return;
    }
    initMut.mutate(
      { username: values.username.trim(), name: values.name.trim(), passwordCipher, keyId },
      {
        onSuccess: () => {
          setDone(true);
        },
        onError: (err) => {
          const code = err instanceof ApiError ? err.code : undefined;
          if (code === ErrCode.SystemAlreadyInitialized) {
            toast.error(t('errAlreadyInit'));
            void navigate({ to: '/login' });
            return;
          }
          if (code === ErrCode.EnvironmentNotReady) {
            toast.error(t('errEnvNotReady'));
            void statusQ.refetch();
            return;
          }
          if (code === ErrCode.UsernameExists) {
            toast.error(t('errUsernameExists'));
            return;
          }
          if (code === ErrCode.PasswordInvalid) {
            toast.error(t('errPasswordInvalid'));
            return;
          }
          if (code === ErrCode.BadRequest) {
            toast.error(t('errBadRequest'));
            return;
          }
          toast.error(t('errGeneric'));
        },
      },
    );
  };

  const blockSubmit = statusQ.data?.block_submit ?? false;
  const submitDisabled = blockSubmit || initMut.isPending || pubQ.isError;

  if (done) {
    return (
      <AuthSplitLayout
        heroEyebrow={t('authHeroEyebrowSetup', { ns: 'common' })}
        heroTitle={t('setupHeroTitle', { ns: 'common' })}
        heroTagline={t('setupHeroTagline', { ns: 'common' })}
        formEyebrow={t('authFormEyebrowDone', { ns: 'common' })}
        formTitle={t('doneTitle')}
        formSubtitle={t('doneDesc')}
      >
        <div className="flex flex-col items-center gap-5 rounded-xl border border-border bg-card p-8 text-center shadow-xs">
          <div className="flex size-12 items-center justify-center rounded-full bg-success/10">
            <CheckCircle2 className="size-6 text-success" />
          </div>
          <p className="text-sm text-muted-foreground">{t('doneDesc')}</p>
          <Button
            type="button"
            size="lg"
            className="w-full rounded-full"
            onClick={() => void navigate({ to: '/login' })}
          >
            {t('goLogin')}
          </Button>
        </div>
      </AuthSplitLayout>
    );
  }

  return (
    <AuthSplitLayout
      heroEyebrow={t('authHeroEyebrowSetup', { ns: 'common' })}
      heroTitle={t('setupHeroTitle', { ns: 'common' })}
      heroTagline={t('setupHeroTagline', { ns: 'common' })}
      heroMeta={statusQ.data?.db_type?.toUpperCase()}
      formEyebrow={t('authFormEyebrowSetup', { ns: 'common' })}
      formTitle={t('setupTitle')}
      formSubtitle={t('setupSubtitle')}
      footer={
        blockSubmit ? (
          <p className="text-center text-sm text-muted-foreground">{t('envBlockedHint')}</p>
        ) : undefined
      }
    >
      <ChecksView
        show={!!statusQ.data}
        dbType={statusQ.data?.db_type}
        dbConnected={statusQ.data?.checks.database.connected}
        redisConnected={statusQ.data?.checks.redis.connected}
        jwtSecretSecure={statusQ.data?.checks.jwt_secret.secure}
        t={t}
      />

      <form onSubmit={handleSubmit(onSubmit)} className="mt-6 flex flex-col gap-4">
        {pubQ.isError && (
          <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2.5 text-sm">
            <span className="text-destructive">{t('publicKeyError')}</span>
            <Button
              type="button"
              variant="link"
              size="sm"
              className="h-auto shrink-0 px-0"
              onClick={() => void pubQ.refetch()}
              disabled={pubQ.isFetching}
            >
              {t('retry', { ns: 'errorBoundary' })}
            </Button>
          </div>
        )}

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-2">
            <Label htmlFor="username" className="text-sm font-medium">
              {t('username')}
            </Label>
            <Input
              id="username"
              autoComplete="username"
              placeholder={t('usernamePlaceholder')}
              className="h-10"
              {...register('username')}
            />
            {errors.username && (
              <p className="text-destructive text-sm">{errors.username.message}</p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="name" className="text-sm font-medium">
              {t('name')}
            </Label>
            <Input
              id="name"
              autoComplete="name"
              placeholder={t('namePlaceholder')}
              className="h-10"
              {...register('name')}
            />
            {errors.name && <p className="text-destructive text-sm">{errors.name.message}</p>}
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="password" className="text-sm font-medium">
            {t('password')}
          </Label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            placeholder={t('passwordPlaceholder')}
            className="h-10"
            {...register('password')}
          />
          {errors.password && (
            <p className="text-destructive text-sm">{errors.password.message}</p>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="confirmPassword" className="text-sm font-medium">
            {t('confirmPassword')}
          </Label>
          <Input
            id="confirmPassword"
            type="password"
            autoComplete="new-password"
            placeholder={t('confirmPasswordPlaceholder')}
            className="h-10"
            {...register('confirmPassword')}
          />
          {errors.confirmPassword && (
            <p className="text-destructive text-sm">{errors.confirmPassword.message}</p>
          )}
        </div>

        <Button
          type="submit"
          size="lg"
          className="mt-2 w-full rounded-full"
          disabled={submitDisabled}
        >
          {initMut.isPending ? t('submitting') : t('submit')}
        </Button>
      </form>
    </AuthSplitLayout>
  );
}

/** 环境就绪自检区：database/redis 阻断项 + JWT 密钥安全性信息项。 */
function ChecksView({
  show,
  dbType,
  dbConnected,
  redisConnected,
  jwtSecretSecure,
  t,
}: {
  show: boolean;
  dbType?: string;
  dbConnected?: boolean;
  redisConnected?: boolean;
  /** JWT 密钥安全性信息项，后端 GetStatus 恒下发。 */
  jwtSecretSecure?: boolean;
  t: (key: string, opts?: Record<string, unknown>) => string;
}) {
  if (!show) return null;
  return (
    <div className="rounded-lg border border-border bg-card p-4 shadow-xs">
      <div className="mb-3 flex items-center justify-between">
        <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
          {t('checksTitle')}
        </span>
        {dbType && (
          <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-foreground/60">
            {dbType}
          </span>
        )}
      </div>
      <ul className="flex flex-col gap-2.5 text-sm">
        <CheckItem ok={!!dbConnected} label={t('checkDatabase')} passText={t('checkPass')} failText={t('checkFail')} />
        <CheckItem ok={!!redisConnected} label={t('checkRedis')} passText={t('checkPass')} failText={t('checkFail')} />
        {jwtSecretSecure !== undefined && (
          <CheckItem
            ok={jwtSecretSecure}
            label={t('checkJwtSecret')}
            passText={t('checkJwtPass')}
            failText={t('checkJwtInsecure')}
            failTone="warning"
          />
        )}
      </ul>
    </div>
  );
}

function CheckItem({
  ok,
  label,
  passText,
  failText,
  failTone = 'destructive',
}: {
  ok: boolean;
  label: string;
  passText: string;
  failText: string;
  /** 失败态色调：阻断项 destructive，非阻断风险项（如 JWT 密钥）warning。 */
  failTone?: 'destructive' | 'warning';
}) {
  return (
    <li className="flex items-center gap-2.5">
      {ok ? (
        <CheckCircle2 className="size-4 shrink-0 text-success" />
      ) : failTone === 'warning' ? (
        <AlertCircle className="size-4 shrink-0 text-warning" />
      ) : (
        <XCircle className="size-4 shrink-0 text-destructive" />
      )}
      <span className="text-foreground">{label}</span>
      <span
        className={cn(
          'ml-auto font-mono text-[11px] uppercase tracking-[0.14em]',
          ok ? 'text-success' : failTone === 'warning' ? 'text-warning' : 'text-destructive'
        )}
      >
        {ok ? passText : failText}
      </span>
    </li>
  );
}
