import { httpClient, ApiError } from '@/lib/http-client';
import type {
  ProfileDetail,
  ProfileFilter,
  ProfileListPage,
  ProfilePeriodRange,
} from '@/lib/contracts';

/** 导出失败文件名回退值（Content-Disposition 解析失败时兜底）。 */
const EXPORT_FALLBACK_FILENAME = '人员画像名单.xlsx';

/** 从 Content-Disposition 的 filename*=UTF-8'' 段解出文件名，解析失败回退默认名（03 §1.6）。 */
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

/** GET /api/profiles：列表（httpClient 解包直取 data）。undefined 字段不发。 */
export async function fetchProfiles(filter: ProfileFilter): Promise<ProfileListPage> {
  const { data } = await httpClient.get<ProfileListPage>('/profiles', {
    params: {
      name: filter.name || undefined,
      activity_level: filter.activity_level || undefined,
      dimension_code: filter.dimension_code || undefined,
      unused_only: filter.unused_only || undefined,
      page: filter.page,
      page_size: filter.page_size,
    },
  });
  return data;
}

/** GET /api/profiles/detail：详情；period 缺省取最新区间。 */
export async function fetchProfileDetail(
  staffName: string,
  period?: ProfilePeriodRange,
): Promise<ProfileDetail> {
  const { data } = await httpClient.get<ProfileDetail>('/profiles/detail', {
    params: {
      staff_name: staffName,
      period_start: period?.period_start,
      period_end: period?.period_end,
    },
  });
  return data;
}

/**
 * GET /api/profiles/export：xlsx 下载。成功返回 Blob 与服务端文件名；失败为统一 JSON 错误结构。
 * 注意主路径：业务错误（1305/1400 等）经 HTTP 200 + application/json 返回（仅 1003 是 401），
 * 拦截器对无 code 字段的 blob 直接放行且 resolve，axios 不会抛错——成功分支必须先判
 * blob.type 含 application/json，是则 text() 解析抛 ApiError；catch 分支仅兜底 401/网络层错误
 * 与已被拦截器 reject 的 blob（03 §1.6 前端按 Content-Type 判别）。
 */
export async function exportProfiles(
  filter: Omit<ProfileFilter, 'page' | 'page_size'>,
): Promise<{ blob: Blob; filename: string }> {
  try {
    const resp = await httpClient.get<Blob>('/profiles/export', {
      params: {
        name: filter.name || undefined,
        activity_level: filter.activity_level || undefined,
        dimension_code: filter.dimension_code || undefined,
        unused_only: filter.unused_only || undefined,
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
