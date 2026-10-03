import {
  useMutation,
  useQuery,
  type UseMutationResult,
  type UseQueryResult,
} from '@tanstack/react-query';

import { ApiError } from '@/lib/http-client';
import type {
  ProfileDetail,
  ProfileFilter,
  ProfileListPage,
  ProfilePeriodRange,
} from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import { exportProfiles, fetchProfileDetail, fetchProfiles } from './api';

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

/** useProfileList：列表查询。queryKey ['profile','list',filter]，token 门控，无轮询（specs §4.1.3）。 */
export function useProfileList(
  filter: ProfileFilter,
): UseQueryResult<ProfileListPage, ApiError> {
  const token = useAuthStore((s) => s.token);
  return useQuery<ProfileListPage, ApiError>({
    queryKey: ['profile', 'list', filter],
    queryFn: () => fetchProfiles(filter),
    enabled: !!token,
  });
}

/** useProfileDetail：详情查询。queryKey ['profile','detail',staffName,period?]。
 * 区间切换由调用方改 period 触发新 queryKey，loading 态防新旧混渲染（specs §4.2.5）。 */
export function useProfileDetail(
  staffName: string,
  period?: ProfilePeriodRange,
): UseQueryResult<ProfileDetail, ApiError> {
  const token = useAuthStore((s) => s.token);
  return useQuery<ProfileDetail, ApiError>({
    queryKey: ['profile', 'detail', staffName, period],
    queryFn: () => fetchProfileDetail(staffName, period),
    enabled: !!token,
  });
}

/** useProfileExport：导出 mutation，isPending 驱动按钮 loading（specs §4.1.3）。
 * 成功即下载，文件名用服务端 filename（03 §1.6 RFC 5987）。 */
export function useProfileExport(): UseMutationResult<
  { blob: Blob; filename: string },
  ApiError,
  Omit<ProfileFilter, 'page' | 'page_size'>
> {
  return useMutation<
    { blob: Blob; filename: string },
    ApiError,
    Omit<ProfileFilter, 'page' | 'page_size'>
  >({
    mutationFn: exportProfiles,
    onSuccess: ({ blob, filename }) => triggerDownload(blob, filename),
  });
}
