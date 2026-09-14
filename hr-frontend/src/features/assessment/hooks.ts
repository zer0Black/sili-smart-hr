import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import type { BatchListPage, BatchPlan, BatchStats, CreateBatchResult } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import {
  createBatch,
  fetchBatchFailures,
  fetchBatchPlan,
  fetchBatchStats,
  fetchBatches,
} from './api';
import type { BatchFailures, BatchFilter, CreateBatchPayload } from './types';

/** 页面可见性状态：specs §4.1.3 隐藏暂停轮询、恢复即拉。 */
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

const POLLING_INTERVAL_MS = 10_000;

/**
 * useBatches：批次列表。polling 为 true（或 'probe'）且页面可见时 10s 轮询。
 * 'probe' 模式供页面级探针：数据里存在非停滞 running 批次时才续轮询，全终态即停，
 * 让轮询开关随真实状态收敛（specs §4.1.3 存在进行中批次才轮询）。
 */
export function useBatches(filter: BatchFilter, opts?: { polling?: boolean | 'probe' }) {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery<BatchListPage>({
    queryKey: ['assessment', 'batches', filter],
    queryFn: () => fetchBatches(filter),
    enabled: !!token,
    refetchInterval: (query) => resolveInterval(query.state.data, opts, visible),
  });
}

function resolveInterval(
  data: BatchListPage | undefined,
  opts: { polling?: boolean | 'probe' } | undefined,
  visible: boolean,
): number | false {
  if (!visible || !opts?.polling) return false;
  if (opts.polling === true) return POLLING_INTERVAL_MS;
  // probe：由数据驱动自收敛
  const hasRunning = (data?.list ?? []).some((b) => b.status === 'running' && !b.stalled);
  return hasRunning ? POLLING_INTERVAL_MS : false;
}

/** useBatchStats：跑批态势统计，随列表同节奏轮询。 */
export function useBatchStats(opts?: { polling?: boolean }) {
  const token = useAuthStore((s) => s.token);
  const visible = usePageVisible();
  return useQuery<BatchStats>({
    queryKey: ['assessment', 'stats'],
    queryFn: fetchBatchStats,
    enabled: !!token,
    refetchInterval: opts?.polling && visible ? POLLING_INTERVAL_MS : false,
  });
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

/** useBatchFailures：批次失败明细，选中批次后启用。 */
export function useBatchFailures(batchId: string | null) {
  return useQuery<BatchFailures>({
    queryKey: ['assessment', 'failures', batchId],
    queryFn: () => fetchBatchFailures(batchId as string),
    enabled: !!batchId,
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
