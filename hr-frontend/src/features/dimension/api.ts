import { httpClient } from '@/lib/http-client';
import type {
  ActivityRule,
  CreateDimensionPayload,
  DeleteDimensionPayload,
  DimensionDetail,
  DimensionMutationResult,
  DimensionTreeNode,
  SaveActivityRulePayload,
  UpdateDimensionPayload,
} from '@/lib/contracts';

/** GET /api/dimensions/tree：维度树全量查询。 */
export async function fetchDimensionTree(): Promise<DimensionTreeNode> {
  const { data } = await httpClient.get<DimensionTreeNode>('/dimensions/tree');
  return data;
}

/** GET /api/dimensions/:id：单维度详情。 */
export async function fetchDimensionDetail(id: string): Promise<DimensionDetail> {
  const { data } = await httpClient.get<DimensionDetail>(`/dimensions/${id}`);
  return data;
}

/** POST /api/dimensions/create：新增维度。 */
export async function createDimension(
  payload: CreateDimensionPayload,
): Promise<DimensionMutationResult> {
  const { data } = await httpClient.post<DimensionMutationResult>(
    '/dimensions/create',
    payload,
  );
  return data;
}

/** POST /api/dimensions/update：编辑维度（含启停）。 */
export async function updateDimension(
  payload: UpdateDimensionPayload,
): Promise<DimensionMutationResult> {
  const { data } = await httpClient.post<DimensionMutationResult>(
    '/dimensions/update',
    payload,
  );
  return data;
}

/** POST /api/dimensions/delete：删除维度（软删除）。 */
export async function deleteDimension(payload: DeleteDimensionPayload): Promise<void> {
  await httpClient.post('/dimensions/delete', payload);
}

/** GET /api/dimensions/activity-rule：活跃度规则查询。 */
export async function fetchActivityRule(): Promise<ActivityRule> {
  const { data } = await httpClient.get<ActivityRule>('/dimensions/activity-rule');
  return data;
}

/** POST /api/dimensions/activity-rule/save：活跃度规则保存。 */
export async function saveActivityRule(
  payload: SaveActivityRulePayload,
): Promise<ActivityRule> {
  const { data } = await httpClient.post<ActivityRule>(
    '/dimensions/activity-rule/save',
    payload,
  );
  return data;
}
