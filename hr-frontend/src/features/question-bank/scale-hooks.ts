// 量表引入域 hooks：候选列表与引入 mutation。
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query';

import type { ImportScaleResult, ScaleCandidate } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import { fetchScales, importScale } from './scale-api';

/** useScales：量表候选列表，弹窗打开（open）且有 token 时才查。 */
export function useScales(
  open: boolean,
): UseQueryResult<{ list: ScaleCandidate[]; total: number }> {
  const token = useAuthStore((s) => s.token);
  return useQuery<{ list: ScaleCandidate[]; total: number }>({
    queryKey: ['question-bank', 'scales'],
    queryFn: fetchScales,
    enabled: open && !!token,
  });
}

/** useImportScale：引入九型量表，成功后废弃 ['question-bank']（候选置灰与批次卡区一并刷新）。 */
export function useImportScale(): UseMutationResult<ImportScaleResult, Error, string> {
  const qc = useQueryClient();
  return useMutation<ImportScaleResult, Error, string>({
    mutationFn: (scaleKey) => importScale({ scale_key: scaleKey }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-bank'] });
    },
  });
}
