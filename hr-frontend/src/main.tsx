import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider, createRouter } from '@tanstack/react-router';

import '@/i18n/config';
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '@/styles/globals.css';

import { AppErrorBoundary } from '@/components/error-boundary';
import { setAppRouter } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';
import { routeTree } from './routeTree.gen';

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
});

// 全局类型注册：恢复 to/search/params 的字面量校验。必须紧跟 createRouter 之后。
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

setAppRouter(router);

const rootEl = document.getElementById('root');
if (!rootEl) {
  throw new Error('root element #root not found');
}

createRoot(rootEl, {
  // 路由边界之外的未捕获错误上报通道：仅做日志，不负责渲染 fallback；
  // fallback 由 AppErrorBoundary 提供。接入监控平台时在此扩展。
  onUncaughtError: (error, info) => {
    console.error('[createRoot] uncaught error:', error, info.componentStack);
  },
}).render(
  <StrictMode>
    <AppErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </AppErrorBoundary>
  </StrictMode>
);
