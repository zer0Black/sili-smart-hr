// 员工作答页 hooks（specs P2_TST_002 §4.1.3/§5.1.5）。T3 先落上下文查询，T4 追加 reply/submit 两 mutation。
import { useQuery } from '@tanstack/react-query';

import { fetchAnswerContext } from './answer-api';

/**
 * useAnswerContext：作答上下文查询。staleTime 0 + retry 0 只发一次，
 * 失败经页面重试按钮显式 refetch（specs §5.1.5：重试重新走校验与上报）。
 */
export function useAnswerContext(token: string) {
  return useQuery({
    queryKey: ['answer', 'context', token],
    queryFn: () => fetchAnswerContext(token),
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
}
