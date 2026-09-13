import { httpClient } from '@/lib/http-client';
import type {
  BatchListPage,
  BatchPlan,
  BatchStats,
  CreateBatchResult,
} from '@/lib/contracts';

import type {
  BatchFailures,
  BatchTargets,
  CreateBatchPayload,
} from './types';

/** GET /api/assessment/batches：批次记录分页查询（按触发时间倒序）。 */
export async function fetchBatches(params: {
  trigger_type?: string;
  status?: string;
  page: number;
  page_size: number;
}): Promise<BatchListPage> {
  const { data } = await httpClient.get<BatchListPage>('/assessment/batches', {
    params: {
      trigger_type: params.trigger_type || undefined,
      status: params.status || undefined,
      page: params.page,
      page_size: params.page_size,
    },
  });
  return data;
}

/** GET /api/assessment/batches/stats：跑批态势统计。 */
export async function fetchBatchStats(): Promise<BatchStats> {
  const { data } = await httpClient.get<BatchStats>('/assessment/batches/stats');
  return data;
}

/** GET /api/assessment/batches/plan：跑批计划（下次触发时点与评估对象）。 */
export async function fetchBatchPlan(): Promise<BatchPlan> {
  const { data } = await httpClient.get<BatchPlan>('/assessment/batches/plan');
  return data;
}

/** GET /api/assessment/batches/targets：批次评估对象全量名单与评估时段（补跑预填数据源）。 */
export async function fetchBatchTargets(batchId: string): Promise<BatchTargets> {
  const { data } = await httpClient.get<BatchTargets>('/assessment/batches/targets', {
    params: { batch_id: batchId },
  });
  return data;
}

/** GET /api/assessment/batches/failures：批次失败明细。 */
export async function fetchBatchFailures(batchId: string): Promise<BatchFailures> {
  const { data } = await httpClient.get<BatchFailures>('/assessment/batches/failures', {
    params: { batch_id: batchId },
  });
  return data;
}

/** POST /api/assessment/batches/create：创建手动批次（手动定向分析与失败补跑共用入口）。 */
export async function createBatch(payload: CreateBatchPayload): Promise<CreateBatchResult> {
  const { data } = await httpClient.post<CreateBatchResult>(
    '/assessment/batches/create',
    payload,
  );
  return data;
}
