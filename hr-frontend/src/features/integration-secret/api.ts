import { httpClient } from '@/lib/http-client';
import type {
  IntegrationSecretView,
  IntegrationSecretDetail,
  UpdateSecretPayload,
  SecretTestResult,
} from '@/lib/contracts';

/** GET /api/integration-secret：返回集成密钥脱敏视图，configured 表示是否已配置过密钥。 */
export async function fetchIntegrationSecret(): Promise<IntegrationSecretView> {
  const { data } = await httpClient.get<IntegrationSecretView>(
    '/integration-secret',
  );
  return data;
}

/** GET /api/integration-secret/detail：返回明文密钥，仅临时查看时调用，关闭即丢弃。 */
export async function fetchSecretDetail(): Promise<IntegrationSecretDetail> {
  const { data } = await httpClient.get<IntegrationSecretDetail>(
    '/integration-secret/detail',
  );
  return data;
}

/** POST /api/integration-secret/update：更新密钥，secret 须前端 RSA-OAEP 加密，keyId 独立传递。 */
export async function updateIntegrationSecret(
  payload: UpdateSecretPayload,
): Promise<{ id: string; configured: boolean; version: number }> {
  const { data } = await httpClient.post<{ id: string; configured: boolean; version: number }>(
    '/integration-secret/update',
    payload,
  );
  return data;
}

/** POST /api/integration-secret/test：测试当前密钥与兄弟系统的连通性。 */
export async function testIntegrationSecret(): Promise<SecretTestResult> {
  const { data } = await httpClient.post<SecretTestResult>(
    '/integration-secret/test',
  );
  return data;
}
