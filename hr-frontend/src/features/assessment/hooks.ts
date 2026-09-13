import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import type { BatchListPage, BatchPlan, BatchStats, CreateBatchResult } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import {
  createBatch,
  fetchBatchFailures,
  fetchBatchPlan,
  fetchBatchStats,
  fetchBatchTargets,
  fetchBatches,
} from './api';
import type { BatchFailures, BatchFilter, BatchTargets, CreateBatchPayload } from './types';

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

/** useBatches：批次列表。polling=true 且页面可见时 10s 轮询，页面隐藏即暂停、恢复立拉。 */
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

/** useBatchTargets：批次评估对象全量名单，选中批次后启用。 */
export function useBatchTargets(batchId: string | null) {
  return useQuery<BatchTargets>({
    queryKey: ['assessment', 'targets', batchId],
    queryFn: () => fetchBatchTargets(batchId as string),
    enabled: !!batchId,
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
