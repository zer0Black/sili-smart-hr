import { useMutation, useQuery } from '@tanstack/react-query';

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
import { queryClient } from '@/lib/query-client';
import { useAuthStore } from '@/stores/auth';

import {
  createDimension,
  deleteDimension,
  fetchActivityRule,
  fetchDimensionDetail,
  fetchDimensionTree,
  saveActivityRule,
  updateDimension,
} from './api';

/** useDimensionTree：维度树全量查询，无 token 时禁用。 */
export function useDimensionTree() {
  const token = useAuthStore((s) => s.token);
  return useQuery<DimensionTreeNode>({
    queryKey: ['dimension', 'tree'],
    queryFn: fetchDimensionTree,
    enabled: !!token,
  });
}

/** useDimensionDetail：单维度详情，无 token 或无 id 时禁用。 */
export function useDimensionDetail(id: string | null | undefined) {
  const token = useAuthStore((s) => s.token);
  return useQuery<DimensionDetail>({
    queryKey: ['dimension', 'detail', id],
    queryFn: () => fetchDimensionDetail(id as string),
    enabled: !!token && !!id,
  });
}

/** useActivityRule：活跃度规则查询，无 token 时禁用。 */
export function useActivityRule() {
  const token = useAuthStore((s) => s.token);
  return useQuery<ActivityRule>({
    queryKey: ['dimension', 'activity-rule'],
    queryFn: fetchActivityRule,
    enabled: !!token,
  });
}

// 维度写操作 mutation：成功后统一废弃 ['dimension','tree']。

export function useCreateDimension() {
  return useMutation<DimensionMutationResult, Error, CreateDimensionPayload>({
    mutationFn: createDimension,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['dimension', 'tree'] });
    },
  });
}

export function useUpdateDimension() {
  return useMutation<DimensionMutationResult, Error, UpdateDimensionPayload>({
    mutationFn: updateDimension,
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: ['dimension', 'tree'] });
      void queryClient.invalidateQueries({ queryKey: ['dimension', 'detail', variables.id] });
    },
  });
}

export function useDeleteDimension() {
  return useMutation<void, Error, DeleteDimensionPayload>({
    mutationFn: deleteDimension,
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({ queryKey: ['dimension', 'tree'] });
      void queryClient.invalidateQueries({ queryKey: ['dimension', 'detail', variables.id] });
    },
  });
}

export function useSaveActivityRule() {
  return useMutation<ActivityRule, Error, SaveActivityRulePayload>({
    mutationFn: saveActivityRule,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['dimension', 'activity-rule'] });
    },
  });
}
