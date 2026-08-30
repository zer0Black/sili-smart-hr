import { QueryClient } from '@tanstack/react-query';

/** TanStack Query 全局配置：适度 staleTime、关窗聚焦重拉、单次重试。 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 5 * 60_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
    mutations: {
      retry: 0,
    },
  },
});
