import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import type {
  AssessmentConfig,
  AssessmentMutationResult,
  SaveAssessmentPayload,
  StaffListPage,
} from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import {
  fetchAssessmentConfig,
  fetchStaffs,
  saveAssessmentConfig,
} from './api';

/** useAssessmentConfig：加载评估周期配置，无 token 时禁用。 */
export function useAssessmentConfig() {
  const token = useAuthStore((s) => s.token);
  return useQuery<AssessmentConfig>({
    queryKey: ['assessment-config'],
    queryFn: fetchAssessmentConfig,
    enabled: !!token,
  });
}

/** useSaveAssessmentConfig：保存后废弃 ['assessment-config'] 缓存以触发刷新。 */
export function useSaveAssessmentConfig() {
  const qc = useQueryClient();
  return useMutation<AssessmentMutationResult, Error, SaveAssessmentPayload>({
    mutationFn: saveAssessmentConfig,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['assessment-config'] });
    },
  });
}

/** useStaffs：分页查询员工列表，无 token 时禁用。 */
export function useStaffs(
  keyword: string,
  page: number,
  pageSize: number,
) {
  const token = useAuthStore((s) => s.token);
  return useQuery<StaffListPage>({
    queryKey: ['staffs', keyword, page, pageSize],
    queryFn: () => fetchStaffs({ keyword, page, pageSize }),
    enabled: !!token,
  });
}
