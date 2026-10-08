import {
  useMutation,
  useQuery,
  type UseMutationResult,
  type UseQueryResult,
} from '@tanstack/react-query';

import { ApiError } from '@/lib/http-client';
import { useAuthStore } from '@/stores/auth';

import { exportOperationLogs, fetchOperationLogs } from './api';
import type { OperationLogFilter, OperationLogPage } from './types';

/** 触发浏览器下载：objectURL 在 click 后下一轮任务 revoke，避免同步 revoke 中断下载。 */
function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url));
}

/** useOperationLogList：列表查询。queryKey ['operation-log','list',filter]，token 门控，无轮询（specs §4.1.3）。 */
export function useOperationLogList(
  filter: OperationLogFilter,
): UseQueryResult<OperationLogPage, ApiError> {
  const token = useAuthStore((s) => s.token);
  return useQuery<OperationLogPage, ApiError>({
    queryKey: ['operation-log', 'list', filter],
    queryFn: () => fetchOperationLogs(filter),
    enabled: !!token,
  });
}

/** useOperationLogExport：导出 mutation，isPending 驱动按钮 loading（specs §4.1.3）。
 * 成功即下载，文件名用服务端 filename（03 §3 A2 RFC 5987）。 */
export function useOperationLogExport(): UseMutationResult<
  { blob: Blob; filename: string },
  ApiError,
  Omit<OperationLogFilter, 'page' | 'page_size'>
> {
  return useMutation<
    { blob: Blob; filename: string },
    ApiError,
    Omit<OperationLogFilter, 'page' | 'page_size'>
  >({
    mutationFn: exportOperationLogs,
    onSuccess: ({ blob, filename }) => triggerDownload(blob, filename),
  });
}
