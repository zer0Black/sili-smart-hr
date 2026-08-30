import { zodResolver } from '@hookform/resolvers/zod';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { AuthSplitLayout } from '@/components/auth-split-layout';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useLogin, usePublicKey } from '@/features/account/hooks';
import { encryptPasswordFresh } from '@/lib/crypto';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { useAuthStore } from '@/stores/auth';

export const Route = createFileRoute('/login')({
  component: LoginPage,
});

function LoginPage() {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const login = useLogin();
  // 进入登录页自动拉取公钥作健康探测；失败时禁用提交并提供重试。
  const pubQ = usePublicKey();

  const schema = z.object({
    username: z.string().min(1, t('usernameRequired')),
    password: z.string().min(1, t('passwordRequired')),
  });
  type LoginForm = z.infer<typeof schema>;

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginForm>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  });

  const onSubmit = async (values: LoginForm) => {
    let passwordCipher: string;
    let keyId: string;
    try {
      // keyId 解密即删，每次提交实时拉新公钥。
      ({ passwordCipher, keyId } = await encryptPasswordFresh(values.password));
    } catch {
      toast.error(t('errGeneric'));
      void pubQ.refetch();
      return;
    }
    login.mutate(
      { username: values.username.trim(), passwordCipher, keyId },
      {
        onSuccess: (data) => {
          setAuth(data.token, data.account);
          toast.success(t('welcome'));
          void navigate({ to: '/' });
        },
        onError: (err) => {
          // 登录路径统一返回 invalid credentials，前端不区分失败原因。
          const code = err instanceof ApiError ? err.code : undefined;
          const msg = code === ErrCode.InvalidCredentials ? t('errCredentials') : t('errGeneric');
          toast.error(msg);
          void pubQ.refetch();
        },
      },
    );
  };

  return (
    <AuthSplitLayout
      heroEyebrow={t('authHeroEyebrowLogin', { ns: 'common' })}
      heroTitle={t('loginHeroTitle', { ns: 'common' })}
      heroTagline={t('appTagline', { ns: 'common' })}
      formEyebrow={t('authFormEyebrowLogin', { ns: 'common' })}
      formTitle={t('loginTitle')}
      formSubtitle={t('loginSubtitle')}
      footer={
        <div className="flex items-center justify-center border-t border-border pt-5">
          <Button
            type="button"
            variant="link"
            className="h-auto p-0 text-sm text-muted-foreground hover:text-foreground"
            onClick={() => toast.info(t('forgotPasswordHint'))}
          >
            {t('forgotPassword')}
          </Button>
        </div>
      }
    >
      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-5">
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
          <Label htmlFor="password" className="text-sm font-medium">
            {t('password')}
          </Label>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            placeholder={t('passwordPlaceholder')}
            className="h-10"
            {...register('password')}
          />
          {errors.password && (
            <p className="text-destructive text-sm">{errors.password.message}</p>
          )}
        </div>
        <Button
          type="submit"
          size="lg"
          className="mt-2 w-full rounded-full"
          disabled={login.isPending || pubQ.isError}
        >
          {login.isPending ? t('submitting') : t('submit')}
        </Button>
      </form>
    </AuthSplitLayout>
  );
}
