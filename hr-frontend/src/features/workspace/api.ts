import { httpClient } from '@/lib/http-client';
import type { WorkspaceData } from '@/lib/contracts';

/** GET /api/workspace：工作台全量聚合（03 W1），无查询参数。 */
export async function fetchWorkspace(): Promise<WorkspaceData> {
  const { data } = await httpClient.get<WorkspaceData>('/workspace');
  return data;
}
