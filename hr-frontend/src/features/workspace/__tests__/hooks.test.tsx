import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

import { useAuthStore } from '@/stores/auth';
import type { WorkspaceAttentionRow, WorkspaceData } from '@/lib/contracts';

vi.mock('../api', () => ({
  fetchWorkspace: vi.fn(),
}));

import { fetchWorkspace } from '../api';
import { useWorkspace } from '../hooks';
import { buildAttentionReason } from '../types';

afterEach(() => {
  useAuthStore.setState({ token: null });
  vi.clearAllMocks();
});

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  });
}

function renderWithClient<T>(callback: () => T, qc: QueryClient) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return renderHook(callback, { wrapper });
}

const emptyData: WorkspaceData = {
  batch: {
    status: null,
    next_trigger_at: null,
    alert_count: 0,
    overdue_count: null,
    data_updated_at: null,
  },
  current_period: null,
  trend: null,
  profile: null,
  attention: null,
};

const formatModule = (m: 'AI_USAGE' | 'AI_MGMT') =>
  m === 'AI_USAGE' ? 'AI 使用能力' : 'AI 管理能力';

const dimNames: Record<string, string> = {
  AI_UPPER_PLAN: '任务规划',
  AI_BASE_LOGIC: '逻辑推理',
  MGT_DELEGATION: '授权分工',
};
const formatDimension = (code: string) => dimNames[code] ?? code;

function makeRow(partial: Partial<WorkspaceAttentionRow>): WorkspaceAttentionRow {
  return {
    staff_name: '李芳',
    category: 'weak',
    activity_level: 'low_freq',
    ai_usage_score: null,
    ai_mgmt_score: null,
    weak_modules: [],
    days_since_active: null,
    ...partial,
  };
}

describe('workspace hooks', () => {
  it('TestUseWorkspace_WithToken：有 token 时发起请求且 queryKey 为 workspace', async () => {
    vi.mocked(fetchWorkspace).mockResolvedValue(emptyData);
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result } = renderWithClient(() => useWorkspace(), qc);

    await waitFor(() => expect(result.current.data).toBeDefined());
    // queryFn 直传 fetchWorkspace，TanStack 注入 context 作首参，只断言次数
    expect(fetchWorkspace).toHaveBeenCalledTimes(1);
    // queryKey 锚点：缓存落在 ['workspace'] 单键上
    expect(qc.getQueryData(['workspace'])).toEqual(emptyData);
  });

  it('TestUseWorkspace_NoToken：无 token 时 enabled 守卫不发起请求', async () => {
    useAuthStore.setState({ token: null });

    const qc = makeClient();
    const { result } = renderWithClient(() => useWorkspace(), qc);

    expect(result.current.fetchStatus).toBe('idle');
    expect(fetchWorkspace).not.toHaveBeenCalled();
    expect(qc.getQueryData(['workspace'])).toBeUndefined();
  });
});

describe('buildAttentionReason', () => {
  it('TestBuildAttentionReason_Weak：双模块 weak 行包含两模块名、得分与短板维度名', () => {
    const row = makeRow({
      category: 'weak',
      ai_usage_score: 52,
      ai_mgmt_score: 45,
      weak_modules: [
        { module: 'AI_USAGE', score: 52, weak_dims: ['AI_UPPER_PLAN', 'AI_BASE_LOGIC'] },
        { module: 'AI_MGMT', score: 45, weak_dims: ['MGT_DELEGATION'] },
      ],
    });

    const reason = buildAttentionReason(row, formatModule, formatDimension);

    expect(reason).toContain('AI 使用能力');
    expect(reason).toContain('52');
    expect(reason).toContain('AI 管理能力');
    expect(reason).toContain('45');
    expect(reason).toContain('任务规划');
    expect(reason).toContain('逻辑推理');
    expect(reason).toContain('授权分工');
    // 多模块与多维度均有分隔，非首尾粘接
    expect(reason).not.toContain('AI 使用能力AI 管理能力');
  });

  it('TestBuildAttentionReason_Unused：unused 行包含最近活跃距今天数', () => {
    const row = makeRow({
      staff_name: '王强',
      category: 'unused',
      activity_level: 'unused',
      days_since_active: 12,
    });

    const reason = buildAttentionReason(row, formatModule, formatDimension);

    expect(reason).toContain('12');
    expect(reason).toContain('天');
  });

  it('TestBuildAttentionReason_WeakEmptyDims：weak 行短板维度为空时不输出短板段', () => {
    const row = makeRow({
      weak_modules: [{ module: 'AI_USAGE', score: 58, weak_dims: [] }],
    });

    const reason = buildAttentionReason(row, formatModule, formatDimension);

    expect(reason).toContain('AI 使用能力');
    expect(reason).toContain('58');
    expect(reason).not.toContain('短板');
  });

  it('TestBuildAttentionReason_EmptyCarriers：载体为空（weak 无模块 / unused 无天数）兜底为空串', () => {
    expect(buildAttentionReason(makeRow({}), formatModule, formatDimension)).toBe('');
    expect(
      buildAttentionReason(
        makeRow({ category: 'unused', days_since_active: null }),
        formatModule,
        formatDimension,
      ),
    ).toBe('');
  });
});
