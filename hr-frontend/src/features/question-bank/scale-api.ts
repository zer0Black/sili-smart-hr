// 量表引入域 API（03_api_interface §3.11/§3.12）：候选列表、引入九型量表。
import { httpClient } from '@/lib/http-client';

import type { ImportScaleResult, ScaleCandidate } from '@/lib/contracts';

/** GET /api/scales：量表候选列表（imported 实时标记，03 §3.11 无分页）。 */
export async function fetchScales(): Promise<{ list: ScaleCandidate[]; total: number }> {
  const { data } = await httpClient.get<{ list: ScaleCandidate[] }>('/scales');
  // 后端 data 用 {list} 包装（无 total），total 前端按 list 长度补齐保持分页契约同构
  return { list: data.list ?? [], total: data.list?.length ?? 0 };
}

/** POST /api/scales/import：引入九型量表，成功形成待审核 IMPORT 批次（03 §3.12）。 */
export async function importScale(payload: { scale_key: string }): Promise<ImportScaleResult> {
  const { data } = await httpClient.post<ImportScaleResult>('/scales/import', payload);
  return data;
}
