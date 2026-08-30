import axios, { type AxiosResponse } from 'axios';
import type { AnyRouter } from '@tanstack/react-router';

import { useAuthStore } from '@/stores/auth';
import { queryClient } from '@/lib/query-client';
import { ErrCode, type Response } from '@/lib/contracts';

/** ApiError 携带后端业务错误码，供消费方分支处理（如登录页区分凭证错误/禁用）。 */
export class ApiError extends Error {
  readonly code: number;
  constructor(code: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
  }
}

const instance = axios.create({
  // 相对 /api：dev 经 rsbuild proxy 转发，生产经 Nginx 同域反代，前端不硬编码后端地址。
  baseURL: '/api',
  timeout: 30_000,
  headers: { 'Content-Type': 'application/json' },
});

// 路由实例由 main 注入，用于 401 时无刷新跳转 login。
let appRouter: AnyRouter | null = null;
export function setAppRouter(router: AnyRouter) {
  appRouter = router;
}

// 请求拦截器：附 Authorization。
instance.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token;
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

// 未授权统一处理：清 token、清查询缓存（避免换号或登出后展示前账号数据）、跳转登录。
function handleUnauthorized() {
  useAuthStore.getState().logout();
  queryClient.clear();
  if (appRouter) {
    appRouter.navigate({ to: '/login' });
  } else {
    window.location.assign('/login');
  }
}

// 响应拦截器：按统一响应结构解包，code!==0 抛 ApiError；未授权（401 或 200+code1003）清缓存跳 login。
instance.interceptors.response.use(
  (resp: AxiosResponse<Response>) => {
    const body = resp.data;
    if (!body || typeof body.code !== 'number') {
      return resp;
    }
    // 统一响应结构下的未授权（HTTP 200 + code 1003）与 HTTP 401 等价处理，消除契约二义性。
    if (body.code === ErrCode.Unauthorized) {
      handleUnauthorized();
      return Promise.reject(new ApiError(ErrCode.Unauthorized, body.message));
    }
    if (body.code !== ErrCode.Success) {
      return Promise.reject(new ApiError(body.code, body.message));
    }
    // 解包：把 resp.data 替换为业务 payload，消费方拿到的即是 data。
    (resp as AxiosResponse).data = body.data;
    return resp;
  },
  (error) => {
    const resp = error?.response;
    const status = resp?.status;
    if (status === 401) {
      handleUnauthorized();
      return Promise.reject(new ApiError(ErrCode.Unauthorized, 'unauthorized'));
    }
    // HTTP 错误但响应体仍是统一结构：读出业务码构造 ApiError，避免后端返回的 code 被丢弃。
    const body = resp?.data as Response | undefined;
    if (body && typeof body.code === 'number') {
      return Promise.reject(new ApiError(body.code, body.message));
    }
    return Promise.reject(error);
  }
);

export const httpClient = instance;
