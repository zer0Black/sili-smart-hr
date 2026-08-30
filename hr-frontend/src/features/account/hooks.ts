import { useMutation, useQuery } from '@tanstack/react-query';

import type {
  AccountListPage,
  AccountMutationResult,
  CreateAccountPayload,
  LoginResult,
  MeResult,
  PublicKeyResult,
  ResetPasswordPayload,
  ToggleEnabledPayload,
  UpdateAccountPayload,
} from '@/lib/contracts';
import { queryClient } from '@/lib/query-client';
import { fetchPublicKey } from '@/lib/crypto';
import { useAuthStore } from '@/stores/auth';

import {
  createAccount,
  deleteAccount,
  fetchAccounts,
  fetchMe,
  loginRequest,
  resetPassword,
  toggleEnabled,
  updateAccount,
} from './api';
import type { LoginPayload } from './types';

/** useLogin：换账号登录后废弃旧 ['me'] 缓存。 */
export function useLogin() {
  return useMutation<LoginResult, Error, LoginPayload>({
    mutationFn: loginRequest,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['me'] });
    },
  });
}

/** useMe：无 token 时禁用。 */
export function useMe() {
  const token = useAuthStore((s) => s.token);
  return useQuery<MeResult>({
    queryKey: ['me'],
    queryFn: fetchMe,
    enabled: !!token,
  });
}

/**
 * usePublicKey：staleTime 240s 略小于后端 expiresIn 300s，缓存命中期间公钥始终在有效期内。
 * 仅作健康探测用，实际加密每次由 encryptPasswordFresh 实时拉新公钥（keyId 一次性）。
 */
export function usePublicKey() {
  return useQuery<PublicKeyResult>({
    queryKey: ['auth', 'public-key'],
    queryFn: fetchPublicKey,
    staleTime: 240_000,
  });
}

/** useAccounts：分页查询账号台账，无 token 时禁用。 */
export function useAccounts(params: {
  page: number;
  pageSize: number;
  keyword?: string;
}) {
  const token = useAuthStore((s) => s.token);
  return useQuery<AccountListPage>({
    queryKey: ['accounts', params.page, params.pageSize, params.keyword ?? ''],
    queryFn: () => fetchAccounts(params),
    enabled: !!token,
  });
}

// 账号写操作 mutation：成功后统一废弃 ['accounts'] 列表缓存。

export function useCreateAccount() {
  return useMutation<AccountMutationResult, Error, CreateAccountPayload>({
    mutationFn: createAccount,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
  });
}

export function useUpdateAccount() {
  return useMutation<AccountMutationResult, Error, UpdateAccountPayload>({
    mutationFn: updateAccount,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
  });
}

export function useDeleteAccount() {
  return useMutation<{ id: string }, Error, string>({
    mutationFn: deleteAccount,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
  });
}

export function useToggleEnabled() {
  return useMutation<{ id: string; enabled: boolean }, Error, ToggleEnabledPayload>({
    mutationFn: toggleEnabled,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
  });
}

export function useResetPassword() {
  return useMutation<{ id: string }, Error, ResetPasswordPayload>({
    mutationFn: resetPassword,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
  });
}
