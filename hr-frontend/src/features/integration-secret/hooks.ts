import { useMutation, useQuery } from '@tanstack/react-query';

import type {
  IntegrationSecretView,
  IntegrationSecretDetail,
  UpdateSecretPayload,
  SecretTestResult,
} from '@/lib/contracts';
import { queryClient } from '@/lib/query-client';
import { useAuthStore } from '@/stores/auth';

import {
  fetchIntegrationSecret,
  fetchSecretDetail,
  updateIntegrationSecret,
  testIntegrationSecret,
} from './api';

/** useIntegrationSecret：加载集成密钥脱敏卡片，无 token 时禁用。 */
export function useIntegrationSecret() {
  const token = useAuthStore((s) => s.token);
  return useQuery<IntegrationSecretView>({
    queryKey: ['integration-secret'],
    queryFn: fetchIntegrationSecret,
    enabled: !!token,
  });
}

/** useSecretDetail：临时查看明文密钥，enabled 由调用方控制（眼睛图标点击时切 true 触发拉取）。 */
export function useSecretDetail(enabled: boolean) {
  const token = useAuthStore((s) => s.token);
  return useQuery<IntegrationSecretDetail>({
    queryKey: ['integration-secret', 'detail'],
    queryFn: fetchSecretDetail,
    enabled: !!token && enabled,
  });
}

/** useUpdateIntegrationSecret：更新密钥，成功后刷新卡片掩码（不 invalidate detail，明文缓存由调用方 removeQueries 管）。 */
export function useUpdateIntegrationSecret() {
  return useMutation<
    { id: string; configured: boolean; version: number },
    Error,
    UpdateSecretPayload
  >({
    mutationFn: updateIntegrationSecret,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['integration-secret'] });
    },
  });
}

/** useSecretTest：连通性测试，结果就地反馈，不变更缓存。 */
export function useSecretTest() {
  return useMutation<SecretTestResult, Error, void>({
    mutationFn: testIntegrationSecret,
  });
}
