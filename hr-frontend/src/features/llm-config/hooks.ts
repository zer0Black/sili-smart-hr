import { useMutation, useQuery } from '@tanstack/react-query';

import type {
  CreateLLMPayload,
  LLMConfigDetail,
  LLMConfigItem,
  LLMDeleteResult,
  LLMEnableResult,
  UpdateLLMPayload,
} from '@/lib/contracts';
import { queryClient } from '@/lib/query-client';
import { useAuthStore } from '@/stores/auth';

import {
  createLLMConfig,
  deleteLLMConfig,
  enableLLMConfig,
  fetchLLMConfigs,
  fetchLLMDetail,
  updateLLMConfig,
} from './api';

/** useLLMConfigs：加载模型列表，无 token 时禁用。 */
export function useLLMConfigs(keyword: string) {
  const token = useAuthStore((s) => s.token);
  return useQuery<LLMConfigItem[]>({
    queryKey: ['llm-configs', keyword],
    queryFn: () => fetchLLMConfigs(keyword || undefined),
    enabled: !!token,
  });
}

/** useLLMDetail：查看弹窗加载模型详情，无 id 时禁用。 */
export function useLLMDetail(id: string | null) {
  const token = useAuthStore((s) => s.token);
  return useQuery<LLMConfigDetail>({
    queryKey: ['llm-configs', 'detail', id],
    queryFn: () => fetchLLMDetail(id as string),
    enabled: !!token && !!id,
  });
}

// 模型写操作 mutation：成功后统一废弃 ['llm-configs'] 前缀，刷新列表与详情。

export function useCreateLLMConfig() {
  return useMutation<{ id: string }, Error, CreateLLMPayload>({
    mutationFn: createLLMConfig,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['llm-configs'] });
    },
  });
}

export function useUpdateLLMConfig() {
  return useMutation<{ id: string }, Error, UpdateLLMPayload>({
    mutationFn: updateLLMConfig,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['llm-configs'] });
    },
  });
}

export function useDeleteLLMConfig() {
  return useMutation<LLMDeleteResult, Error, string>({
    mutationFn: deleteLLMConfig,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['llm-configs'] });
    },
  });
}

export function useEnableLLMConfig() {
  return useMutation<LLMEnableResult, Error, string>({
    mutationFn: enableLLMConfig,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['llm-configs'] });
    },
  });
}
