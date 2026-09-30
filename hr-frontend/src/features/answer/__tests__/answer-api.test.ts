// 作答页专用请求通道测试：解包、业务错误、无 Authorization、无 401 登出、429/网络错误通道级语义。
import axios, { AxiosError, type AxiosAdapter, type InternalAxiosRequestConfig } from 'axios';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useAuthStore } from '@/stores/auth';
import { ErrCode } from '@/lib/contracts';

import { AnswerApiError, answerClient, fetchAnswerContext, sendAnswerReply, submitAnswer } from '../answer-api';

// 手写 axios adapter stub（项目无 msw/axios-mock-adapter）：捕获经拦截器链后的请求配置，
// 按用例注入响应；非 2xx 仿 axios settle 语义以 AxiosError reject，走响应侧 onRejected 分支。
type Respond = (config: InternalAxiosRequestConfig) => { status: number; data: unknown };

function installAdapter(respond: Respond): InternalAxiosRequestConfig[] {
  const seen: InternalAxiosRequestConfig[] = [];
  const adapter: AxiosAdapter = async (config) => {
    seen.push(config);
    const r = respond(config);
    const response = {
      data: r.data,
      status: r.status,
      statusText: '',
      headers: {},
      config,
      request: {},
    };
    if (r.status < 200 || r.status >= 300) {
      throw new axios.AxiosError(
        `Request failed with status code ${r.status}`,
        String(r.status),
        config,
        {},
        response,
      );
    }
    return response;
  };
  answerClient.defaults.adapter = adapter;
  return seen;
}

const token = 'tk-43-chars-aaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const account = { id: '1', username: 'admin', name: 'Admin', enabled: true };

afterEach(() => {
  vi.restoreAllMocks();
  useAuthStore.setState({ token: null, account: null });
  // 还原被用例替换的 adapter，避免实例状态跨文件语义泄漏。
  delete answerClient.defaults.adapter;
});

describe('answerClient 解包与业务错误', () => {
  it('TestUnwrapSuccess：code=0 解包 data，fetchAnswerContext 返回业务载荷', async () => {
    const seen = installAdapter(() => ({
      status: 200,
      data: { code: 0, message: 'ok', data: { task_no: 'T1' } },
    }));
    const result = await fetchAnswerContext(token);
    expect(result).toEqual({ task_no: 'T1' });
    expect(seen[0].url).toBe('/answer/context');
    expect(seen[0].method).toBe('post');
  });

  it('TestBusinessErrorThrows：code=1901 抛 AnswerApiError 且 err.code===1901', async () => {
    installAdapter(() => ({
      status: 200,
      data: { code: 1901, message: 'answer token invalid' },
    }));
    const err = await fetchAnswerContext(token).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AnswerApiError);
    expect((err as AnswerApiError).code).toBe(ErrCode.AnswerTokenInvalid);
    expect((err as AnswerApiError).message).toBe('answer token invalid');
  });

  it('TestHttpErrorWithUnifiedBody：HTTP 500 带统一结构体读出业务码 1500 构造 AnswerApiError', async () => {
    installAdapter(() => ({
      status: 500,
      data: { code: 1500, message: 'internal error' },
    }));
    const err = await fetchAnswerContext(token).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AnswerApiError);
    expect((err as AnswerApiError).code).toBe(1500);
    expect((err as AnswerApiError).message).toBe('internal error');
  });

  it('TestUnwrapFallbackOnNonUnifiedBody：响应体无 code 字段时原样透传不解包', async () => {
    installAdapter(() => ({ status: 200, data: '<html>proxy error</html>' }));
    const result = await fetchAnswerContext(token).catch((e: unknown) => e);
    expect(result).toBe('<html>proxy error</html>');
  });
});

describe('answerClient 无鉴权与无登出分支（specs §4.1.6）', () => {
  it('TestNoAuthHeader：auth store 有 token 时请求头仍无 Authorization', async () => {
    useAuthStore.setState({ token: 'platform-jwt-token', account });
    const seen = installAdapter(() => ({
      status: 200,
      data: { code: 0, message: 'ok', data: { task_no: 'T1' } },
    }));
    await fetchAnswerContext(token);
    expect(seen[0].headers.Authorization).toBeUndefined();
    expect(useAuthStore.getState().token).toBe('platform-jwt-token');
  });

  it('TestNoLogoutOnUnauthorized：HTTP 200 + code 1003 不触发 logout，仅抛 AnswerApiError code=1003', async () => {
    useAuthStore.setState({ token: 'platform-jwt-token', account });
    const logoutSpy = vi.spyOn(useAuthStore.getState(), 'logout');
    installAdapter(() => ({
      status: 200,
      data: { code: 1003, message: 'unauthorized' },
    }));
    const err = await fetchAnswerContext(token).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AnswerApiError);
    expect((err as AnswerApiError).code).toBe(1003);
    expect(logoutSpy).not.toHaveBeenCalled();
    expect(useAuthStore.getState().token).toBe('platform-jwt-token');
    expect(window.location.pathname).not.toBe('/login');
  });

  it('TestNoLogoutOnHttp401：HTTP 401 不触发 logout 与 /login 跳转，非统一结构体按 axios 原样透传', async () => {
    useAuthStore.setState({ token: 'platform-jwt-token', account });
    const logoutSpy = vi.spyOn(useAuthStore.getState(), 'logout');
    installAdapter(() => ({ status: 401, data: 'Unauthorized' }));
    const err = await fetchAnswerContext(token).catch((e: unknown) => e);
    // httpClient 兜底口径：HTTP 错误且非统一结构体时透传原始 AxiosError（本域无 401 业务语义）。
    expect(err).toBeInstanceOf(AxiosError);
    expect((err as AxiosError).response?.status).toBe(401);
    expect(logoutSpy).not.toHaveBeenCalled();
    expect(useAuthStore.getState().token).toBe('platform-jwt-token');
    expect(window.location.pathname).not.toBe('/login');
  });
});

describe('三请求函数 body 组装', () => {
  it('TestContextBody：fetchAnswerContext 发 POST /answer/context，body 只含 token', async () => {
    const seen = installAdapter(() => ({
      status: 200,
      data: { code: 0, message: 'ok', data: null },
    }));
    await fetchAnswerContext(token);
    expect(seen[0].url).toBe('/answer/context');
    expect(JSON.parse(seen[0].data as string)).toEqual({ token });
  });

  it('TestReplyBody：sendAnswerReply 发 POST /answer/reply，body 只携带 token/content（服务端题号权威）', async () => {
    const seen = installAdapter(() => ({
      status: 200,
      data: {
        code: 0,
        message: 'ok',
        data: {
          question_seq: 3,
          answered_count: 3,
          question_total: 5,
          action: 'next',
          next_question: { seq: 4, dimension_name: '监督验收', scenario: 's', requirement: 'r' },
        },
      },
    }));
    const result = await sendAnswerReply(token, 'C');
    expect(seen[0].url).toBe('/answer/reply');
    expect(JSON.parse(seen[0].data as string)).toEqual({ token, content: 'C' });
    expect(result.action).toBe('next');
    expect(result.next_question?.seq).toBe(4);
  });

  it('TestReplyFinishedUnwrap：action=finished 解包 next_question=null', async () => {
    const seen = installAdapter(() => ({
      status: 200,
      data: {
        code: 0,
        message: 'ok',
        data: { question_seq: 1, answered_count: 1, question_total: 5, action: 'finished', next_question: null },
      },
    }));
    const result = await sendAnswerReply(token, '5');
    expect(JSON.parse(seen[0].data as string)).toEqual({ token, content: '5' });
    expect(result.action).toBe('finished');
    expect(result.next_question).toBeNull();
  });

  it('TestSubmitBody：submitAnswer 发 POST /answer/submit，body 只含 token 并解包 task_no', async () => {
    const seen = installAdapter(() => ({
      status: 200,
      data: { code: 0, message: 'ok', data: { task_no: 'T202609290001' } },
    }));
    const result = await submitAnswer(token);
    expect(result).toEqual({ task_no: 'T202609290001' });
    expect(seen[0].url).toBe('/answer/submit');
    expect(JSON.parse(seen[0].data as string)).toEqual({ token });
  });
});

describe('通道级错误保留 code=0（429 与网络错误）', () => {
  it('Test429KeepsZeroCode：HTTP 429 不采纳响应体业务码，AnswerApiError code=0', async () => {
    // 后端限流中间件 429 响应体携带统一结构 code=1500（ratelimit.go），若读体 code 会漂成 1500。
    installAdapter(() => ({
      status: 429,
      data: { code: 1500, message: 'too many requests' },
    }));
    const err = await sendAnswerReply(token, 'C').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AnswerApiError);
    expect((err as AnswerApiError).code).toBe(0);
    expect((err as AnswerApiError).message).toBe('too many requests');
  });

  it('TestNetworkErrorKeepsZeroCode：无响应的网络错误归一为 code=0 的 AnswerApiError', async () => {
    installAdapter(() => {
      throw new axios.AxiosError('Network Error', axios.AxiosError.ERR_NETWORK);
    });
    const err = await fetchAnswerContext(token).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AnswerApiError);
    expect((err as AnswerApiError).code).toBe(0);
    expect((err as AnswerApiError).message).toContain('网络');
  });
});
