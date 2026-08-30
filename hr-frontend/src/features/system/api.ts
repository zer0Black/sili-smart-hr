import { httpClient } from '@/lib/http-client';
import type {
  HealthResult,
  InitializePayload,
  InitializeResult,
  SetupStatus,
  SystemSummary,
} from '@/lib/contracts';

/** GET /api/setup/status：返回初始化状态与环境自检结果。 */
export async function fetchSetupStatus(): Promise<SetupStatus> {
  const { data } = await httpClient.get<SetupStatus>('/setup/status');
  return data;
}

/** POST /api/setup/initialize：创建首个账号并锁定初始化状态。 */
export async function initialize(
  payload: InitializePayload,
): Promise<InitializeResult> {
  const { data } = await httpClient.post<InitializeResult>(
    '/setup/initialize',
    payload,
  );
  return data;
}

/** GET /api/system/status：返回运行状态摘要。 */
export async function fetchSystemSummary(): Promise<SystemSummary> {
  const { data } = await httpClient.get<SystemSummary>('/system/status');
  return data;
}

/** POST /api/system/health-check：触发组件健康测试。 */
export async function runHealthCheck(): Promise<HealthResult> {
  const { data } = await httpClient.post<HealthResult>('/system/health-check');
  return data;
}
