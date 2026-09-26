import { useEffect } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import type { BatchListPage, BatchPlan, BatchStats, CreateBatchResult } from '@/lib/contracts';
import { usePageVisible, subscribeVisibility } from '@/lib/use-page-visible';
import { useAuthStore } from '@/stores/auth';

import {
  createBatch,
  fetchBatchFailures,
  fetchBatchPlan,
  fetchBatchStats,
  fetchBatches,
} from './api';
import type { BatchFailures, BatchFilter, CreateBatchPayload } from './types';

const POLLING_INTERVAL_MS = 10_000;

/**
 * useBatches：批次列表。polling 为 true 且页面可见时 10s 轮询（specs §4.1.3）。
 * 轮询开关由页面级 stats 探针（running_batch_count）驱动，口径与统计卡同源，
 * 不受翻页/筛选把 running 批次挤出当前页影响。
 */
export function useBatches(filter: BatchFilter, opts?: { polling?: boolean }) {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery<BatchListPage>({
    queryKey: ['assessment', 'batches', filter],
    queryFn: () => fetchBatches(filter),
    enabled: !!token,
    refetchInterval: opts?.polling && visible ? POLLING_INTERVAL_MS : false,
  });
}

/**
 * useBatchStats：跑批态势统计，自驱动轮询：数据里存在进行中批次
 *（running_batch_count > 0，已剔除停滞，与列表 stalled 同口径）时 10s 续轮询，
 * 全终态即停。轮询期跨域废弃（batches/stats）同步触发统计卡刷新。
 * isError 时继续轮询：running_batch_count 探针一次瞬时失败就把轮询永久关停
 * 会让进度条冻结且无自愈（refetchInterval 在 data 缺席时返 false 即停摆）。
 */
export function useBatchStats() {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery<BatchStats>({
    queryKey: ['assessment', 'stats'],
    queryFn: fetchBatchStats,
    enabled: !!token,
    refetchInterval: (query) => {
      if (!visible) return false;
      const d = query.state.data;
      if (d) {
        return d.running_batch_count > 0 ? POLLING_INTERVAL_MS : false;
      }
      // 错误态继续轮询自愈：探针一次瞬时失败就把轮询永久关停会让进度条冻结
      //（pending 正常路径一次间隔后必然有 data，无需轮询）。
      return query.state.status === 'error' ? POLLING_INTERVAL_MS : false;
    },
  });
}

/** useRefetchOnVisible：恢复即拉（specs §4.1.3 后半句）。
 * refetchOnWindowFocus 全局关闭，切回标签页时显式 refetch 一次，
 * 让恢复瞬间的数据即时而非等下一个轮询间隔。 */
export function useRefetchOnVisible(
  queries: Array<{ refetch: () => Promise<unknown> }>,
) {
  const refetchAll = () => {
    for (const q of queries) void q.refetch();
  };
  useEffect(() => {
    const unsubscribe = subscribeVisibility(refetchAll);
    return unsubscribe;
    // refetch 引用稳定，refetchAll 只需挂一次
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}

/** useBatchPlan：跑批计划卡，不轮询。 */
export function useBatchPlan() {
  const token = useAuthStore((s) => s.token);
  return useQuery<BatchPlan>({
    queryKey: ['assessment', 'plan'],
    queryFn: fetchBatchPlan,
    enabled: !!token,
  });
}

/** useBatchFailures：批次失败明细，选中批次后启用（token 守卫对齐同文件其余 hook 口径）。 */
export function useBatchFailures(batchId: string | null) {
  const token = useAuthStore((s) => s.token);
  return useQuery<BatchFailures>({
    queryKey: ['assessment', 'failures', batchId],
    queryFn: () => fetchBatchFailures(batchId as string),
    enabled: !!batchId && !!token,
  });
}

/** useCreateBatch：发起手动批次。成功后废弃列表与统计缓存，列表顶部出现新进行中批次。 */
export function useCreateBatch() {
  const qc = useQueryClient();
  return useMutation<CreateBatchResult, Error, CreateBatchPayload>({
    mutationFn: createBatch,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['assessment', 'batches'] });
      void qc.invalidateQueries({ queryKey: ['assessment', 'stats'] });
    },
  });
}
