import { httpClient } from '@/lib/http-client';
import type {
  CreateLLMPayload,
  LLMConfigDetail,
  LLMConfigItem,
  LLMDeleteResult,
  LLMEnableResult,
  UpdateLLMPayload,
} from '@/lib/contracts';

/** GET /api/llm-configs：返回全部模型，keyword 可选按名称或模型 ID 模糊过滤。 */
export async function fetchLLMConfigs(
  keyword?: string,
): Promise<LLMConfigItem[]> {
  const { data } = await httpClient.get<LLMConfigItem[]>('/llm-configs', {
    params: keyword ? { keyword } : undefined,
  });
  return data;
}

/** POST /api/llm-configs/create：新增模型，api_key 须前端 RSA-OAEP 加密。 */
export async function createLLMConfig(
  payload: CreateLLMPayload,
): Promise<{ id: string }> {
  const { data } = await httpClient.post<{ id: string }>(
    '/llm-configs/create',
    payload,
  );
  return data;
}

/** POST /api/llm-configs/update：编辑模型，api_key 留空表示不改密钥。 */
export async function updateLLMConfig(
  payload: UpdateLLMPayload,
): Promise<{ id: string }> {
  const { data } = await httpClient.post<{ id: string }>(
    '/llm-configs/update',
    payload,
  );
  return data;
}

/** POST /api/llm-configs/delete：物理删除，返回转启目标 id（无转启为 null）。 */
export async function deleteLLMConfig(
  id: string,
): Promise<LLMDeleteResult> {
  const { data } = await httpClient.post<LLMDeleteResult>(
    '/llm-configs/delete',
    { id },
  );
  return data;
}

/** POST /api/llm-configs/enable：排他启用目标模型。 */
export async function enableLLMConfig(
  id: string,
): Promise<LLMEnableResult> {
  const { data } = await httpClient.post<LLMEnableResult>(
    '/llm-configs/enable',
    { id },
  );
  return data;
}

/** GET /api/llm-configs/{id}：查询模型详情，含 API Key 明文，关闭弹窗即丢弃。 */
export async function fetchLLMDetail(id: string): Promise<LLMConfigDetail> {
  const { data } = await httpClient.get<LLMConfigDetail>(
    `/llm-configs/${id}`,
  );
  return data;
}
