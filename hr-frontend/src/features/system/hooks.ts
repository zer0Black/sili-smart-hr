import { useMutation, useQuery } from '@tanstack/react-query';

import type {
  HealthResult,
  InitializePayload,
  InitializeResult,
  SetupStatus,
  SystemSummary,
} from '@/lib/contracts';
import { queryClient } from '@/lib/query-client';
import { ApiError } from '@/lib/http-client';
import { ErrCode } from '@/lib/contracts';

import { fetchSetupStatus, fetchSystemSummary, initialize, runHealthCheck } from './api';

/**
 * useSetupStatus：staleTime Infinity，首访探针在 __root beforeLoad 用 fetchQuery
 * 预取并缓存，向导页直接命中缓存，避免重复请求。
 */
export function useSetupStatus() {
  return useQuery<SetupStatus>({
    queryKey: ['setup', 'status'],
    queryFn: fetchSetupStatus,
    staleTime: Infinity,
  });
}

/**
 * useInitialize：成功或收到 1101（系统已初始化）都用 setQueryData 同步把缓存置为已初始化态。
 * 用 setQueryData 而非 invalidateQueries：__root beforeLoad 的 fetchQuery 带调用级 staleTime:Infinity，
 * invalidate 触发的后台重取不阻塞下一次 fetchQuery；setQueryData 直接改缓存值，
 * 紧随其后的 navigate('/login') → beforeLoad 立即读到 initialized:true，不会被旧值甩回 /setup。
 */
export function useInitialize() {
  return useMutation<InitializeResult, Error, InitializePayload>({
    mutationFn: initialize,
    onSuccess: () => {
      queryClient.setQueryData<SetupStatus>(['setup', 'status'], (old) => ({
        ...(old as SetupStatus),
        initialized: true,
      }));
    },
    onError: (err) => {
      // 1101 表示系统已被（可能是另一个 tab）初始化，缓存置位避免 beforeLoad 用旧 initialized:false 弹回 /setup。
      if (err instanceof ApiError && err.code === ErrCode.SystemAlreadyInitialized) {
        queryClient.setQueryData<SetupStatus>(['setup', 'status'], (old) => ({
          ...(old as SetupStatus),
          initialized: true,
        }));
      }
    },
  });
}

/** useSystemSummary：运行状态摘要，受 _authenticated 布局保护，token 由拦截器自动附。 */
export function useSystemSummary() {
  return useQuery<SystemSummary>({
    queryKey: ['system', 'summary'],
    queryFn: fetchSystemSummary,
  });
}

/** useHealthCheck：组件健康测试，按需触发，不自动跑。 */
export function useHealthCheck() {
  return useMutation<HealthResult, Error, void>({
    mutationFn: () => runHealthCheck(),
  });
}
