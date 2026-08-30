import { createFileRoute, Outlet, redirect, useNavigate } from '@tanstack/react-router';
import { Languages, LogOut, Moon, Sun } from 'lucide-react';
import { useTheme } from 'next-themes';
import { useTranslation } from 'react-i18next';

import { NavMenu } from '@/components/nav-menu';
import { Button } from '@/components/ui/button';
import { isTokenExpired } from '@/lib/jwt';
import { queryClient } from '@/lib/query-client';
import { useAuthStore } from '@/stores/auth';

export const Route = createFileRoute('/_authenticated')({
  beforeLoad: () => {
    // 本地校验 token 存在性与过期时间，过期 token 直接拦截，避免登录态闪烁。
    const { token } = useAuthStore.getState();
    if (!token || isTokenExpired(token)) {
      throw redirect({ to: '/login' });
    }
  },
  component: AuthLayout,
});

function AuthLayout() {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();
  const navigate = useNavigate();
  const account = useAuthStore((s) => s.account);
  const logout = useAuthStore((s) => s.logout);

  const toggleLang = () => {
    void i18n.changeLanguage(i18n.language.startsWith('zh') ? 'en' : 'zh');
  };
  const toggleTheme = () => setTheme(theme === 'dark' ? 'light' : 'dark');
  const handleLogout = () => {
    logout();
    queryClient.clear();
    void navigate({ to: '/login' });
  };

  return (
    <div className="min-h-svh">
      <header className="bg-background/80 sticky top-0 z-40 border-b backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-7xl items-center gap-4 px-4">
          <div className="flex shrink-0 items-center gap-2">
            <span className="bg-block-lilac size-2 rounded-full" aria-hidden />
            <span className="font-semibold tracking-tight">{t('appTitle', { ns: 'common' })}</span>
          </div>
          <NavMenu />
          <div className="ml-auto flex items-center gap-1">
            {account && <span className="text-muted-foreground mr-2 text-sm">{account.name}</span>}
            <Button variant="ghost" size="icon" onClick={toggleLang} title={i18n.language.startsWith('zh') ? t('languageEn', { ns: 'common' }) : t('languageZh', { ns: 'common' })}>
              <Languages />
            </Button>
            <Button variant="ghost" size="icon" onClick={toggleTheme} title={t('toggleTheme', { ns: 'common' })}>
              {theme === 'dark' ? <Sun /> : <Moon />}
            </Button>
            <Button variant="ghost" size="icon" onClick={handleLogout} title={t('logout', { ns: 'common' })}>
              <LogOut />
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}
