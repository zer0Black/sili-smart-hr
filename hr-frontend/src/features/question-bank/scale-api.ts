// 量表引入域 API（03_api_interface §3.11/§3.12）：候选列表、引入九型量表。
import { httpClient } from '@/lib/http-client';

import type { ImportScaleResult, ScaleCandidate } from '@/lib/contracts';

/** GET /api/scales：量表候选列表（imported 实时标记，03 §3.11 无分页，data 仅 {list}）。 */
export async function fetchScales(): Promise<{ list: ScaleCandidate[] }> {
  const { data } = await httpClient.get<{ list: ScaleCandidate[] }>('/scales');
  return { list: data.list ?? [] };
}

/** POST /api/scales/import：引入九型量表，成功形成待审核 IMPORT 批次（03 §3.12）。 */
export async function importScale(payload: { scale_key: string }): Promise<ImportScaleResult> {
  const { data } = await httpClient.post<ImportScaleResult>('/scales/import', payload);
  return data;
}
