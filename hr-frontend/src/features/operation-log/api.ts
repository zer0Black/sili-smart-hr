import { httpClient, ApiError } from '@/lib/http-client';

import type { OperationLogFilter, OperationLogPage } from './types';

/** 导出失败文件名回退值（Content-Disposition 解析失败时兜底）。 */
const EXPORT_FALLBACK_FILENAME = '操作日志.xlsx';

/** 从 Content-Disposition 的 filename*=UTF-8'' 段解出文件名，解析失败回退默认名（03 §3 A2）。 */
function parseExportFilename(disposition: string | undefined): string {
  if (disposition) {
    const match = disposition.match(/filename\*=(?:UTF|utf)-8''([^;]+)/);
    if (match?.[1]) {
      try {
        const decoded = decodeURIComponent(match[1]);
        if (decoded) return decoded;
      } catch {
        // 非法百分号序列走回退
      }
    }
  }
  return EXPORT_FALLBACK_FILENAME;
}

/** blob 为统一 JSON 错误体时解析并抛 ApiError；恒抛错，正常二进制流不走此函数。 */
async function rejectJsonBlob(blob: Blob): Promise<never> {
  const text = await blob.text();
  let body: { code?: unknown; message?: unknown } = {};
  try {
    body = JSON.parse(text) as { code?: unknown; message?: unknown };
  } catch {
    // 非法 JSON 也按未知错误抛出，防错误体静默成空 blob
  }
  throw new ApiError(
    typeof body.code === 'number' ? body.code : -1,
    typeof body.message === 'string' ? body.message : 'export failed',
  );
}

/** GET /api/operation-logs：列表（httpClient 解包直取 data）。空筛选字段不发。 */
export async function fetchOperationLogs(
  filter: OperationLogFilter,
): Promise<OperationLogPage> {
  const { data } = await httpClient.get<OperationLogPage>('/operation-logs', {
    params: {
      operator: filter.operator || undefined,
      module: filter.module || undefined,
      result: filter.result || undefined,
      start_date: filter.start_date || undefined,
      end_date: filter.end_date || undefined,
      page: filter.page,
      page_size: filter.page_size,
    },
  });
  return data;
}

/**
 * GET /api/operation-logs/export：xlsx 下载。成功返回 Blob 与服务端文件名；失败为统一 JSON 错误结构。
 * 主路径：业务错误（1400/1500）经 HTTP 200 + application/json 返回（仅 1003 是 401），拦截器对
 * 无 code 字段的 blob 直接放行且 resolve，成功分支必须先判 blob.type 含 application/json；
 * catch 分支兜底 401/网络层错误与已被拦截器 reject 的 blob（03 §3 A2 按 Content-Type 判别，F9 同款）。
 */
export async function exportOperationLogs(
  filter: Omit<OperationLogFilter, 'page' | 'page_size'>,
): Promise<{ blob: Blob; filename: string }> {
  try {
    const resp = await httpClient.get<Blob>('/operation-logs/export', {
      params: {
        operator: filter.operator || undefined,
        module: filter.module || undefined,
        result: filter.result || undefined,
        start_date: filter.start_date || undefined,
        end_date: filter.end_date || undefined,
      },
      responseType: 'blob',
    });
    // HTTP 200 业务错误：拦截器对 blob 放行后 resolve，此处按 Content-Type 判别（主路径）。
    if (resp.data.type.includes('application/json')) {
      await rejectJsonBlob(resp.data);
    }
    const disposition = resp.headers?.['content-disposition'];
    return { blob: resp.data, filename: parseExportFilename(disposition) };
  } catch (error) {
    // 兜底分支：拦截器 reject 的 blob（如 1003 处理后的原始响应）或 HTTP 错误携带 json blob。
    const blob = (error as { response?: { data?: unknown } })?.response?.data;
    if (blob instanceof Blob && blob.type.includes('application/json')) {
      await rejectJsonBlob(blob);
    }
    throw error;
  }
}
