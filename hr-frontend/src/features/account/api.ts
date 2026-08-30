import { httpClient } from '@/lib/http-client';
import type {
  AccountListPage,
  AccountMutationResult,
  CreateAccountPayload,
  LoginResult,
  MeResult,
  ResetPasswordPayload,
  ToggleEnabledPayload,
  UpdateAccountPayload,
} from '@/lib/contracts';

import type { LoginPayload } from './types';

/** POST /api/login：返回 token 与脱敏账号。 */
export async function loginRequest(payload: LoginPayload): Promise<LoginResult> {
  const { data } = await httpClient.post<LoginResult>('/login', payload);
  return data;
}

/** GET /api/me：受保护接口，返回当前账号。 */
export async function fetchMe(): Promise<MeResult> {
  const { data } = await httpClient.get<MeResult>('/me');
  return data;
}

/** GET /api/accounts：分页查询账号列表，keyword 模糊匹配账号与姓名。 */
export async function fetchAccounts(params: {
  page: number;
  pageSize: number;
  keyword?: string;
}): Promise<AccountListPage> {
  const { data } = await httpClient.get<AccountListPage>('/accounts', {
    params: {
      page: params.page,
      page_size: params.pageSize,
      keyword: params.keyword,
    },
  });
  return data;
}

/** POST /api/accounts/create：新增账号。 */
export async function createAccount(
  payload: CreateAccountPayload,
): Promise<AccountMutationResult> {
  const { data } = await httpClient.post<AccountMutationResult>(
    '/accounts/create',
    payload,
  );
  return data;
}

/** POST /api/accounts/update：编辑账号。 */
export async function updateAccount(
  payload: UpdateAccountPayload,
): Promise<AccountMutationResult> {
  const { data } = await httpClient.post<AccountMutationResult>(
    '/accounts/update',
    payload,
  );
  return data;
}

/** POST /api/accounts/delete：删除账号。 */
export async function deleteAccount(id: string): Promise<{ id: string }> {
  const { data } = await httpClient.post<{ id: string }>('/accounts/delete', {
    id,
  });
  return data;
}

/** POST /api/accounts/toggle-enabled：启用/停用账号。 */
export async function toggleEnabled(
  payload: ToggleEnabledPayload,
): Promise<{ id: string; enabled: boolean }> {
  const { data } = await httpClient.post<{ id: string; enabled: boolean }>(
    '/accounts/toggle-enabled',
    payload,
  );
  return data;
}

/** POST /api/accounts/reset-password：重置密码。 */
export async function resetPassword(
  payload: ResetPasswordPayload,
): Promise<{ id: string }> {
  const { data } = await httpClient.post<{ id: string }>(
    '/accounts/reset-password',
    payload,
  );
  return data;
}
