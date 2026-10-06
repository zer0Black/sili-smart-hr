import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { ApiError } from '@/lib/http-client';
import type { WorkspaceData } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import { fetchWorkspace } from './api';

/** useWorkspace：工作台全量查询。页面加载单次触发，无轮询（specs §4.1.3 前端自动流程）。 */
export function useWorkspace(): UseQueryResult<WorkspaceData, ApiError> {
  const token = useAuthStore((s) => s.token);
  return useQuery<WorkspaceData, ApiError>({
    queryKey: ['workspace'],
    queryFn: fetchWorkspace,
    enabled: !!token,
  });
}
