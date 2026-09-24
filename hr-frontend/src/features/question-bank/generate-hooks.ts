// 题目生成域 hooks：发起/轮询/取消的数据通道（03 §3.13/§3.14）。
import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query';

import type { CreateGenerationResult, GenerationProgress } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import { cancelGeneration, createGeneration, fetchGenerationProgress } from './generate-api';

/** 生成轮询间隔（03 §3.14：建议 2s）。 */
const GENERATION_POLL_MS = 2_000;

// visibilitychange 监听（assessment hooks 同款模式）：页面隐藏暂停轮询，恢复续轮。
function usePageVisible(): boolean {
  const [visible, setVisible] = useState(
    () => typeof document === 'undefined' || document.visibilityState === 'visible',
  );
  useEffect(() => {
    const onChange = () => setVisible(document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);
  return visible;
}

/**
 * useGenerationProgress：进度轮询，非终态（QUEUED/RUNNING）且页面可见时 2s 续轮，
 * 终态停（specs §4.3.4 规则 1 同步等待语义，03 §3.14）。
 */
export function useGenerationProgress(id: string | null): UseQueryResult<GenerationProgress> {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery<GenerationProgress>({
    queryKey: ['question-bank', 'generation-progress', id],
    queryFn: () => fetchGenerationProgress(id as string),
    enabled: !!id && !!token,
    refetchInterval: (query) => {
      if (!visible) return false;
      const d = query.state.data;
      // 轮询首拍即见的 QUEUED→RUNNING 迁移也走 2s 节拍；瞬时失败保持自愈续轮（useBatchStats 同理）
      if (d) return d.status === 'QUEUED' || d.status === 'RUNNING' ? GENERATION_POLL_MS : false;
      return query.state.status === 'error' ? GENERATION_POLL_MS : false;
    },
  });
}

/** useCreateGeneration：发起一次生成。 */
export function useCreateGeneration(): UseMutationResult<
  CreateGenerationResult,
  Error,
  { dimension_ids: string[]; count: number }
> {
  return useMutation<CreateGenerationResult, Error, { dimension_ids: string[]; count: number }>({
    mutationFn: createGeneration,
  });
}

/** useCancelGeneration：进行态离开放弃本批（specs §4.3.4 规则 1），终态幂等成功。 */
export function useCancelGeneration(): UseMutationResult<void, Error, string> {
  return useMutation<void, Error, string>({
    mutationFn: cancelGeneration,
  });
}
