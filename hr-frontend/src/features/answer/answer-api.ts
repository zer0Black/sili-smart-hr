// 员工作答页专用请求通道（specs §4.1.6：不读 auth store、不触发 401 登出跳转）。
// httpClient 的无鉴权变体：复制 {code,message,data} 解包，去掉 Authorization 附头与 1003/401 登出分支。
import axios, { type AxiosInstance, type AxiosResponse } from 'axios';

import { ErrCode, type Response } from '@/lib/contracts';

import type { AnswerContextResult, AnswerReplyResult, AnswerSubmitResult } from './answer-types';

/** 作答域业务/通道错误，形态同 ApiError；code=0 表示通道级错误（网络异常、限流 429），非业务码。 */
export class AnswerApiError extends Error {
  readonly code: number;
  constructor(code: number, message: string) {
    super(message);
    this.name = 'AnswerApiError';
    this.code = code;
  }
}

const instance: AxiosInstance = axios.create({
  // 相对 /api：dev 经 rsbuild proxy 转发，生产经 Nginx 同域反代，与 httpClient 同源约定。
  baseURL: '/api',
  timeout: 30_000,
  headers: { 'Content-Type': 'application/json' },
});

// 令牌在 POST body 自证（specs §5.1.4 规则2 公开路由组），本实例不附 Authorization。

// 响应拦截器：统一结构解包，code!==0 抛 AnswerApiError。作答域无 401 语义（无 JWT，令牌失效走 1901），
// 故无 1003/401 登出分支。
instance.interceptors.response.use(
  (resp: AxiosResponse<Response>) => {
    const body = resp.data;
    if (!body || typeof body.code !== 'number') {
      return resp;
    }
    if (body.code !== ErrCode.Success) {
      return Promise.reject(new AnswerApiError(body.code, body.message));
    }
    (resp as AxiosResponse).data = body.data;
    return resp;
  },
  (error) => {
    const resp = error?.response;
    const status = resp?.status;
    // 429 限流与网络错误是通道级错误，保留 code=0（429 响应体携带的统一结构 code 不代表业务语义）。
    if (status === 429 || !resp) {
      const msg = status === 429 ? resp?.data?.message : undefined;
      return Promise.reject(new AnswerApiError(0, msg || '网络异常，请稍后重试'));
    }
    // 其余 HTTP 错误但响应体仍是统一结构：读出业务码构造 AnswerApiError。
    const body = resp?.data as Response | undefined;
    if (body && typeof body.code === 'number') {
      return Promise.reject(new AnswerApiError(body.code, body.message));
    }
    return Promise.reject(error);
  },
);

export const answerClient = instance;

/** POST /api/answer/context：作答页上下文（令牌校验与会话建立，03 §3 A1）。 */
export async function fetchAnswerContext(token: string): Promise<AnswerContextResult> {
  const { data } = await instance.post<AnswerContextResult>('/answer/context', { token });
  return data;
}

/** POST /api/answer/reply：逐题作答回复（03 §3 A2）。服务端按已落库记录数推算题号。 */
export async function sendAnswerReply(token: string, content: string): Promise<AnswerReplyResult> {
  const { data } = await instance.post<AnswerReplyResult>('/answer/reply', {
    token,
    content,
  });
  return data;
}

/** POST /api/answer/submit：提交作答（03 §3 A3），返回任务号供成功态参考编号展示。 */
export async function submitAnswer(token: string): Promise<AnswerSubmitResult> {
  const { data } = await instance.post<AnswerSubmitResult>('/answer/submit', { token });
  return data;
}
