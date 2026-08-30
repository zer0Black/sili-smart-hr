import { httpClient } from '@/lib/http-client';
import type {
  AssessmentConfig,
  AssessmentMutationResult,
  SaveAssessmentPayload,
  StaffListPage,
} from '@/lib/contracts';

/** GET /api/assessment-config：获取评估周期配置。 */
export async function fetchAssessmentConfig(): Promise<AssessmentConfig> {
  const { data } = await httpClient.get<AssessmentConfig>('/assessment-config');
  return data;
}

/** POST /api/assessment-config/save：保存评估周期配置。 */
export async function saveAssessmentConfig(
  payload: SaveAssessmentPayload,
): Promise<AssessmentMutationResult> {
  const { data } = await httpClient.post<AssessmentMutationResult>(
    '/assessment-config/save',
    payload,
  );
  return data;
}

/** GET /api/staffs：分页查询员工列表，keyword 模糊匹配。 */
export async function fetchStaffs(params: {
  keyword?: string;
  page: number;
  pageSize: number;
}): Promise<StaffListPage> {
  const { data } = await httpClient.get<StaffListPage>('/staffs', {
    params: {
      keyword: params.keyword,
      page: params.page,
      page_size: params.pageSize,
    },
  });
  return data;
}
