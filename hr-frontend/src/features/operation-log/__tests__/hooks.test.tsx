// operation-log hooks 测试（验收锚点：token 门控 + 请求 query 含 page/page_size 且空 operator 不发）。
// mock httpClient 而非本域 api：锚点要求断言到请求参数层（axios 对 undefined 值参数不序列化），
// hooks + api 全真实，只替换传输层，与 profile export.test 的 mock 注入形态一致。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

import { useAuthStore } from '@/stores/auth';

const getMock = vi.hoisted(() => vi.fn());

vi.mock('@/lib/http-client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/http-client')>();
  return { ...actual, httpClient: { get: getMock } };
});

import { useOperationLogExport, useOperationLogList } from '../hooks';

// jsdom 的 URL 未实现 objectURL 静态方法，triggerDownload 依赖之，先注入可 spy 的桩。
beforeAll(() => {
  Object.defineProperty(URL, 'createObjectURL', {
    value: vi.fn(() => 'blob:mock-url'),
    configurable: true,
    writable: true,
  });
  Object.defineProperty(URL, 'revokeObjectURL', {
    value: vi.fn(),
    configurable: true,
    writable: true,
  });
});

afterEach(() => {
  useAuthStore.setState({ token: null });
  getMock.mockReset();
  vi.clearAllMocks();
});

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function renderWithClient<T>(callback: () => T, qc: QueryClient) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return renderHook(callback, { wrapper });
}

const emptyPage = { list: [], total: 0, page: 1, page_size: 10 };
const XLSX_TYPE = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';

describe('useOperationLogList', () => {
  it('TestUseOperationLogListParams：有 token 发起请求，query 含 page=1&page_size=10 且空 operator 不出现', async () => {
    getMock.mockResolvedValue({ data: emptyPage });
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result } = renderWithClient(
      () => useOperationLogList({ operator: '', page: 1, page_size: 10 }),
      qc,
    );

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(getMock).toHaveBeenCalledTimes(1);
    expect(getMock.mock.calls[0][0]).toBe('/operation-logs');
    const params = getMock.mock.calls[0][1]?.params as Record<string, unknown>;
    expect(params.page).toBe(1);
    expect(params.page_size).toBe(10);
    // 空串被 api 层收敛为 undefined，axios 序列化时该键不发
    expect(params.operator).toBeUndefined();
    expect(result.current.data?.total).toBe(0);
  });

  it('TestUseOperationLogListDisabledWithoutToken：无 token 时 enabled 守卫不发起请求', async () => {
    getMock.mockResolvedValue({ data: emptyPage });

    const qc = makeClient();
    const { result } = renderWithClient(
      () => useOperationLogList({ page: 1, page_size: 10 }),
      qc,
    );

    expect(result.current.fetchStatus).toBe('idle');
    expect(getMock).not.toHaveBeenCalled();
  });
});

describe('useOperationLogExport', () => {
  it('TestUseOperationLogExportMutate：mutate 透传筛选（无分页键）且成功触发下载', async () => {
    const blob = new Blob(['xlsx'], { type: XLSX_TYPE });
    const encoded = encodeURIComponent('操作日志_20261008_100000.xlsx');
    getMock.mockResolvedValue({
      data: blob,
      headers: { 'content-disposition': `attachment; filename*=UTF-8''${encoded}` },
    });
    useAuthStore.setState({ token: 'test-token' });

    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => undefined);

    const qc = makeClient();
    const { result } = renderWithClient(() => useOperationLogExport(), qc);

    await act(async () => {
      await result.current.mutateAsync({
        operator: '李',
        module: 'dimension',
        start_date: '2026-09-01',
        end_date: '2026-09-30',
      });
    });

    expect(getMock.mock.calls[0][0]).toBe('/operation-logs/export');
    const params = getMock.mock.calls[0][1]?.params as Record<string, unknown>;
    expect(params).toEqual({
      operator: '李',
      module: 'dimension',
      start_date: '2026-09-01',
      end_date: '2026-09-30',
    });
    // 导出接口不携带分页参数
    expect(params).not.toHaveProperty('page');
    expect(params).not.toHaveProperty('page_size');
    // onSuccess 触发浏览器下载：objectURL 创建 + anchor click
    expect(URL.createObjectURL).toHaveBeenCalledWith(blob);
    expect(clickSpy).toHaveBeenCalledTimes(1);

    clickSpy.mockRestore();
  });
});
