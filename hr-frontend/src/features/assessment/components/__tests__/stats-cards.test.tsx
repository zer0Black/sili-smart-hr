import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { BatchPlan, BatchStats } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

const navigateMock = vi.fn();

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}));

vi.mock('@/features/assessment/api', () => ({
  fetchBatchStats: vi.fn(),
  fetchBatchPlan: vi.fn(),
}));

import { fetchBatchPlan, fetchBatchStats } from '@/features/assessment/api';

import { StatsCards } from '../stats-cards';

const STATS_OK: BatchStats = { eval_count: 7, evaluated_person_count: 5, running_batch_count: 2 };

function makePlan(overrides: Partial<BatchPlan> = {}): BatchPlan {
  return {
    next_trigger_at: '2026-09-14 23:00',
    period: 'weekly',
    target_mode: 'all',
    target_brief: [],
    target_names: [],
    target_count: 0,
    dimension_base_count: 4,
    dimension_upper_count: 3,
    ...overrides,
  };
}

function renderStatsCards(node: ReactElement = <StatsCards />) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe('StatsCards 统计卡 + 计划卡', () => {
  it('TestStatsCardsRender：渲染三指标数值与计划卡四字段', async () => {
    vi.mocked(fetchBatchStats).mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan).mockResolvedValue(makePlan());
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    await waitFor(() => {
      expect(screen.getByText('7')).toBeInTheDocument();
    });
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('本期评测次数')).toBeInTheDocument();
    expect(screen.getByText('已完成评估人次')).toBeInTheDocument();
    expect(screen.getByText('进行中批次数')).toBeInTheDocument();

    expect(screen.getByText('2026-09-14 23:00')).toBeInTheDocument();
    expect(screen.getByText('每周')).toBeInTheDocument();
    expect(screen.getByText('全员')).toBeInTheDocument();
    expect(screen.getByText('底层 4 维 + 上层 3 维')).toBeInTheDocument();
  });

  it('TestPlanCardSummary：specified 模式超 2 人显示摘要「等 N 人」并悬浮全量名单', async () => {
    vi.mocked(fetchBatchStats).mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan).mockResolvedValue(
      makePlan({
        target_mode: 'specified',
        target_brief: ['张敏', '李芳'],
        target_names: ['张敏', '李芳', '王强'],
        target_count: 12,
      }),
    );
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    const summary = await screen.findByText('张敏、李芳 等 12 人');
    expect(summary).toHaveAttribute('title', '张敏、李芳、王强');
  });

  it('TestPlanCardSummary：specified 仅 2 人时直显不带「等 N 人」', async () => {
    vi.mocked(fetchBatchStats).mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan).mockResolvedValue(
      makePlan({
        target_mode: 'specified',
        target_brief: ['张敏', '李芳'],
        target_names: ['张敏', '李芳'],
        target_count: 2,
      }),
    );
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    const summary = await screen.findByText('张敏、李芳');
    expect(summary).toHaveAttribute('title', '张敏、李芳');
    expect(screen.queryByText(/等 \d+ 人/)).not.toBeInTheDocument();
  });

  it('TestPlanCardSummary：all + target_count=0 只显「全员」不显人数', async () => {
    vi.mocked(fetchBatchStats).mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan).mockResolvedValue(makePlan());
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    await screen.findByText('全员');
    expect(screen.queryByText(/等 \d+ 人/)).not.toBeInTheDocument();
  });

  it('TestPlanCardFailure：计划卡加载失败态出现刷新按钮，点击触发 refetch', async () => {
    vi.mocked(fetchBatchStats).mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan)
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValue(makePlan());
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    const refreshBtn = await screen.findByRole('button', { name: '刷新' });
    fireEvent.click(refreshBtn);

    await waitFor(() => {
      expect(screen.getByText('2026-09-14 23:00')).toBeInTheDocument();
    });
  });

  it('统计卡加载失败态出现刷新按钮，点击触发 refetch', async () => {
    vi.mocked(fetchBatchStats)
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan).mockResolvedValue(makePlan());
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    await waitFor(() => {
      expect(screen.getAllByRole('button', { name: '刷新' }).length).toBeGreaterThan(0);
    });
    fireEvent.click(screen.getAllByRole('button', { name: '刷新' })[0]);

    await waitFor(() => {
      expect(screen.getByText('7')).toBeInTheDocument();
    });
  });

  it('数据加载完成前渲染骨架，不渲染数值', () => {
    vi.mocked(fetchBatchStats).mockReturnValue(new Promise(() => {}));
    vi.mocked(fetchBatchPlan).mockReturnValue(new Promise(() => {}));
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    expect(screen.queryByText('本期评测次数')).toBeInTheDocument();
    expect(document.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0);
  });

  it('计划卡右侧两跳转按钮分别导航到系统参数页与维度配置页', async () => {
    vi.mocked(fetchBatchStats).mockResolvedValue(STATS_OK);
    vi.mocked(fetchBatchPlan).mockResolvedValue(makePlan());
    useAuthStore.setState({ token: 'test-token' });

    renderStatsCards();

    fireEvent.click(await screen.findByRole('button', { name: '评估周期' }));
    expect(navigateMock).toHaveBeenCalledWith({ to: '/system/params' });

    fireEvent.click(screen.getByRole('button', { name: '维度权重' }));
    expect(navigateMock).toHaveBeenCalledWith({ to: '/system/dimension' });
  });
});
