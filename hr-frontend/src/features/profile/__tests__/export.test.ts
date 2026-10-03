// exportProfiles 导出判别测试（specs §4.1.3 导出 / 03 §1.6 二进制流旁路）：
// 成功透传与 RFC5987 文件名解码、HTTP200+JSON 主路径业务错误、HTTP 错误 catch 兜底。
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AxiosResponse } from 'axios';

import { ApiError, httpClient } from '@/lib/http-client';

import { exportProfiles } from '../api';

// mock httpClient.get：保留 ApiError 真实实现供 instanceof 判别；get 按 axios 拦截器
// 放行后的形态注入（blob 无 code 字段时拦截器原样 resolve，主路径判别在 api 成功分支）。
// jsdom 的 Blob 实现缺 text()（Node 侧才有）：被测 api 依赖 blob.text 读 JSON 错误体。
// 用轻量 stub 替换全局 Blob：内部存字符串，type/size/text 自承载，instanceof 判别用
// stub 构造器兜底（api 的 catch 分支按 blob instanceof Blob 识别）。
vi.hoisted(() => {
  class BlobStub {
    readonly #parts: string[];
    readonly type: string;
    constructor(parts: Array<BlobPart | string>, options?: BlobPropertyBag) {
      this.#parts = parts.map(String);
      this.type = options?.type ?? '';
    }
    get size(): number {
      return this.#parts.join('').length;
    }
    text(): Promise<string> {
      return Promise.resolve(this.#parts.join(''));
    }
  }
  Object.defineProperty(globalThis, 'Blob', {
    value: BlobStub,
    configurable: true,
    writable: true,
  });
});

const getMock = vi.hoisted(() => vi.fn());

vi.mock('@/lib/http-client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/http-client')>();
  return { ...actual, httpClient: { get: getMock } };
});

const XLSX_TYPE = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';

function okResponse(blob: Blob, disposition?: string): AxiosResponse<Blob> {
  return {
    data: blob,
    status: 200,
    statusText: 'OK',
    headers: disposition ? { 'content-disposition': disposition } : {},
    config: { headers: {} },
  } as AxiosResponse<Blob>;
}

/** 统一 JSON 错误体伪装为 blob（后端导出失败时的响应形态，03 §1.6）。 */
function jsonErrorBlob(code: number, message: string): Blob {
  return new Blob([JSON.stringify({ code, message })], { type: 'application/json' });
}

afterEach(() => {
  getMock.mockReset();
});

describe('exportProfiles 成功分支', () => {
  it('TestExportSuccess：xlsx blob 透传 + Content-Disposition 文件名解码', async () => {
    const blob = new Blob(['xlsx-bytes'], { type: XLSX_TYPE });
    const encoded = encodeURIComponent('人员画像名单_20261003.xlsx');
    getMock.mockResolvedValue(okResponse(blob, `attachment; filename*=UTF-8''${encoded}`));

    const result = await exportProfiles({});

    expect(result.blob).toBe(blob);
    expect(result.filename).toBe('人员画像名单_20261003.xlsx');
    expect(getMock).toHaveBeenCalledTimes(1);
    expect(getMock.mock.calls[0][0]).toBe('/profiles/export');
  });

  it('TestExportFilenameFallback：无 Content-Disposition 回退默认文件名', async () => {
    const blob = new Blob(['xlsx-bytes'], { type: XLSX_TYPE });
    getMock.mockResolvedValue(okResponse(blob));

    const result = await exportProfiles({});

    expect(result.filename).toBe('人员画像名单.xlsx');
  });

  it('TestExportFilenameMalformed：非法百分号序列回退默认文件名不抛错', async () => {
    const blob = new Blob(['xlsx-bytes'], { type: XLSX_TYPE });
    getMock.mockResolvedValue(okResponse(blob, "attachment; filename*=UTF-8''%zz%..xlsx"));

    const result = await exportProfiles({});

    expect(result.filename).toBe('人员画像名单.xlsx');
  });

  it('TestExportParams：筛选参数透传，空值字段不发请求', async () => {
    const blob = new Blob(['xlsx-bytes'], { type: XLSX_TYPE });
    getMock.mockResolvedValue(okResponse(blob));

    await exportProfiles({
      name: '张三',
      activity_level: 'active',
      dimension_code: 'AI_COMMUNICATION',
      unused_only: true,
    });

    const params = getMock.mock.calls[0][1]?.params as Record<string, unknown>;
    expect(params).toEqual({
      name: '张三',
      activity_level: 'active',
      dimension_code: 'AI_COMMUNICATION',
      unused_only: true,
    });

    // 空条件导全员：空串与 false 均不进入 params
    await exportProfiles({ name: '', activity_level: '', dimension_code: '', unused_only: false });
    expect(getMock.mock.calls[1][1]?.params).toEqual({});
  });
});

describe('exportProfiles 业务错误主路径（HTTP 200 + application/json blob）', () => {
  it('TestBusinessErrorJsonBlob：1305 抛 ApiError，且先于文件名解析（headers 带 disposition 仍 reject）', async () => {
    const blob = jsonErrorBlob(1305, 'staff list unavailable');
    getMock.mockResolvedValue(
      okResponse(blob, `attachment; filename*=UTF-8''${encodeURIComponent(' decoy.xlsx')}`),
    );

    const err = await exportProfiles({}).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe(1305);
    expect((err as ApiError).message).toBe('staff list unavailable');
  });

  it('TestBusinessErrorMalformedJson：JSON 体无 code 字段时按 -1 兜底抛 ApiError', async () => {
    const blob = new Blob(['{"msg":"weird"}'], { type: 'application/json' });
    getMock.mockResolvedValue(okResponse(blob));

    const err = await exportProfiles({}).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe(-1);
  });
});

describe('exportProfiles HTTP 错误 catch 兜底分支', () => {
  it('TestHttpErrorJsonBlob：error.response.data 为 json blob 抛 ApiError 1305', async () => {
    const blob = jsonErrorBlob(1305, 'staff list unavailable');
    getMock.mockRejectedValue({ response: { status: 200, data: blob } });

    const err = await exportProfiles({}).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe(1305);
    expect((err as ApiError).message).toBe('staff list unavailable');
  });

  it('TestHttpErrorNonJson：非 json blob 的原始错误原样 rethrow 不改写', async () => {
    const rawBlob = new Blob(['binary'], { type: XLSX_TYPE });
    const raw = Object.assign(new Error('network down'), {
      response: { status: 502, data: rawBlob },
    });
    getMock.mockRejectedValue(raw);

    const err = await exportProfiles({}).catch((e: unknown) => e);

    expect(err).toBe(raw);
  });
});

// httpClient 引用锁定：确认 mock 注入点与被测模块消费的是同一实例路径。
void httpClient;
