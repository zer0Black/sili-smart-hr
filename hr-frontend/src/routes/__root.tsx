import {
  Outlet,
  createRootRouteWithContext,
  redirect,
} from '@tanstack/react-router';
import type { QueryClient } from '@tanstack/react-query';

import { NotFound, RouteError } from '@/components/error-boundary';
import { ThemeProvider } from '@/components/theme-provider';
import { Toaster } from '@/components/ui/sonner';
import { fetchSetupStatus } from '@/features/system/api';

interface RouterContext {
  queryClient: QueryClient;
}

export const Route = createRootRouteWithContext<RouterContext>()({
  // 首访探针：先于子路由 beforeLoad（如 _authenticated 的 token 校验）执行。
  // 仅在查询成功且 initialized=false 时跳转向导页；查询失败 fail-soft 放行，
  // 由目标路由自身鉴权接管，避免后端瞬态故障把已登录用户甩到公开向导页。
  beforeLoad: async ({ context, location }) => {
    let initialized: boolean;
    try {
      const status = await context.queryClient.fetchQuery({
        queryKey: ['setup', 'status'],
        queryFn: fetchSetupStatus,
        staleTime: Infinity,
      });
      initialized = status.initialized;
    } catch {
      // 网络/5xx 等场景放行到目标路由：setup 探针是一次性首启检测，
      // 不应放大为全站掉线。_authenticated 的 token 校验、401 拦截器、
      // /answer/$token 的令牌鉴权会各自兜住真实未授权态。
      return;
    }
    if (!initialized && location.pathname !== '/setup') {
      throw redirect({ to: '/setup' });
    }
    if (initialized && location.pathname === '/setup') {
      throw redirect({ to: '/login' });
    }
  },
  component: RootComponent,
  // 路由树兜底：组件渲染期崩溃或 loader 意外抛错在此捕获（root 是顶层，错误冒泡到此）。
  errorComponent: RouteError,
  // 无匹配路由的 404 兜底。
  notFoundComponent: NotFound,
});

function RootComponent() {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <Outlet />
      <Toaster />
    </ThemeProvider>
  );
}
