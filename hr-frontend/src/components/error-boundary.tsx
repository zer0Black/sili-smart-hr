import { Component, type ErrorInfo, type ReactNode } from 'react';
import { useNavigate, type ErrorComponentProps } from '@tanstack/react-router';
import { AlertTriangle, FileQuestion, Home, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';

// 生产环境隐藏原始错误细节，dev 下展开可看堆栈，辅助排查。
// 项目无 import.meta.env 消费者，rsbuild 在客户端静态替换 process.env.NODE_ENV。
const isDev = process.env.NODE_ENV !== 'production';

// ---------- 路由外兜底：应用级 ErrorBoundary ----------

interface AppErrorBoundaryProps {
  children: ReactNode;
  /** 自定义 fallback 渲染；不传则使用内置 GlobalErrorFallback。 */
  fallback?: (error: Error, reset: () => void) => ReactNode;
}

interface AppErrorBoundaryState {
  error: Error | null;
}

// 路由树之外的兜底边界。路由树内部的渲染错误由 __root.tsx 的 errorComponent 捕获，
// 这里只兜 RouterProvider 渲染期或路由边界之外的崩溃。
// React 19 仍未提供内置 ErrorBoundary 函数组件，渲染 fallback 仍须 class 组件。
export class AppErrorBoundary extends Component<AppErrorBoundaryProps, AppErrorBoundaryState> {
  state: AppErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): AppErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // createRoot.onUncaughtError 是另一条上报通道；这里捕获到说明已被边界兜住。
    // 接入监控平台时在此扩展，当前仅 console.error。
    console.error('[AppErrorBoundary] 渲染期未捕获错误:', error, info.componentStack);
  }

  private reset = () => this.setState({ error: null });

  render() {
    const { error } = this.state;
    if (error) {
      return this.props.fallback
        ? this.props.fallback(error, this.reset)
        : <GlobalErrorFallback error={error} reset={this.reset} />;
    }
    return this.props.children;
  }
}

// ---------- 共享展示组件 ----------

interface ErrorViewProps {
  icon: ReactNode;
  title: string;
  desc: string;
  retryLabel: string;
  homeLabel: string;
  onRetry: () => void;
  onHome: () => void;
  error?: Error;
  detailsLabel?: string;
}

// 错误兜底页的统一视觉。语义化 token（bg-background 等）保证主题切换下可读；
// 路由外场景下 ThemeProvider 可能尚未挂载，dark class 缺失也不影响基础可读性。
function ErrorView({
  icon,
  title,
  desc,
  retryLabel,
  homeLabel,
  onRetry,
  onHome,
  error,
  detailsLabel,
}: ErrorViewProps) {
  return (
    <div className="bg-background text-foreground flex min-h-svh flex-col items-center justify-center gap-6 p-6 text-center">
      <div className="flex flex-col items-center gap-3">
        {icon}
        <h1 className="text-2xl font-semibold">{title}</h1>
        <p className="text-muted-foreground max-w-md text-sm">{desc}</p>
      </div>
      <div className="flex flex-wrap justify-center gap-2">
        <Button onClick={onRetry}>
          <RefreshCw />
          {retryLabel}
        </Button>
        <Button variant="outline" onClick={onHome}>
          <Home />
          {homeLabel}
        </Button>
      </div>
      {isDev && error && (
        <details className="text-muted-foreground mt-4 max-w-xl text-left text-xs">
          <summary className="cursor-pointer select-none">{detailsLabel ?? 'Details'}</summary>
          <pre className="bg-muted mt-2 overflow-auto rounded-md p-3 break-all whitespace-pre-wrap">
            {error.stack ?? String(error)}
          </pre>
        </details>
      )}
    </div>
  );
}

// ---------- 路由外兜底 UI（ThemeProvider 之外） ----------

// i18n 已在 main.tsx 顶部通过 import '@/i18n/config' 同步初始化（resources 为同步 import），
// useTranslation 在此使用默认 i18n 实例，不依赖 I18nextProvider。
export function GlobalErrorFallback({ error, reset }: { error: Error; reset: () => void }) {
  const { t } = useTranslation('errorBoundary');
  return (
    <ErrorView
      icon={<AlertTriangle className="text-destructive size-12" />}
      title={t('title')}
      desc={t('desc')}
      retryLabel={t('retry')}
      homeLabel={t('goHome')}
      detailsLabel={t('details')}
      error={error}
      onRetry={reset}
      onHome={() => window.location.assign('/')}
    />
  );
}

// ---------- 路由内兜底 UI（__root.tsx 的 errorComponent） ----------

// reset 来自 TanStack CatchBoundary，调用后重新渲染出错的路由组件（重试）；
// navigate 提供回首页入口，走客户端路由。
export function RouteError({ error, reset }: ErrorComponentProps) {
  const { t } = useTranslation('errorBoundary');
  const navigate = useNavigate();
  return (
    <ErrorView
      icon={<AlertTriangle className="text-destructive size-12" />}
      title={t('title')}
      desc={t('desc')}
      retryLabel={t('retry')}
      homeLabel={t('goHome')}
      detailsLabel={t('details')}
      error={error}
      onRetry={reset}
      onHome={() => void navigate({ to: '/' })}
    />
  );
}

// ---------- 404（__root.tsx 的 notFoundComponent） ----------

export function NotFound() {
  const { t } = useTranslation('notFound');
  const navigate = useNavigate();
  return (
    <div className="bg-background text-foreground flex min-h-svh flex-col items-center justify-center gap-6 p-6 text-center">
      <div className="flex flex-col items-center gap-3">
        <FileQuestion className="text-muted-foreground size-12" />
        <h1 className="text-2xl font-semibold">{t('title')}</h1>
        <p className="text-muted-foreground max-w-md text-sm">{t('desc')}</p>
      </div>
      <Button variant="outline" onClick={() => void navigate({ to: '/' })}>
        <Home />
        {t('goHome')}
      </Button>
    </div>
  );
}
