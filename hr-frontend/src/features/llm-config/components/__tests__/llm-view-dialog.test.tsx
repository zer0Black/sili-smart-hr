// LLMViewDialog 弹窗交互测试（specs §4.4 / §4.2.3 / BR1 / BR2）
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import i18n from '@/i18n/config';

// navigator.clipboard.writeText mock
const writeTextMock = vi.fn();
beforeEach(() => {
  writeTextMock.mockReset();
  Object.defineProperty(globalThis.navigator, 'clipboard', {
    configurable: true,
    value: { writeText: writeTextMock },
  });
});

const detailMock = vi.fn();
vi.mock('@/features/llm-config/hooks', () => ({
  useLLMDetail: () => {
    const data = detailMock();
    return {
      data,
      isLoading: false,
      isError: false,
    };
  },
}));

const removeQueriesMock = vi.fn();
vi.mock('@/lib/query-client', () => ({
  queryClient: {
    removeQueries: (...args: unknown[]) => removeQueriesMock(...args),
  },
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import { LLMViewDialog } from '@/features/llm-config/components/llm-view-dialog';
import type { LLMConfigDetail } from '@/lib/contracts';

beforeEach(() => {
  void i18n.changeLanguage('zh');
});

const detail: LLMConfigDetail = {
  id: '1780000000000000101',
  name: '主力模型',
  provider: 'deepseek',
  model_id: 'deepseek-chat',
  api_url: 'https://api.deepseek.com/v1',
  api_key: 'sk-plaintext-secret-key',
  enabled: true,
  version: 1,
  created_at: '2026-08-12T10:00:00Z',
  updated_at: '2026-08-12T10:00:00Z',
};

describe('LLMViewDialog（§4.4 / §4.2.3 / BR1 / BR2）', () => {
  beforeEach(() => {
    detailMock.mockReset();
    detailMock.mockReturnValue(detail);
    removeQueriesMock.mockReset();
  });

  it('渲染标题含模型名称，展示模型名称 / 模型 ID / 服务商 / API 地址 / 启用状态', () => {
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    // 标题
    expect(screen.getByText(`查看模型·${detail.name}`)).toBeInTheDocument();
    // 基本字段
    expect(screen.getByText('主力模型')).toBeInTheDocument();
    expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0);
    expect(screen.getByText('https://api.deepseek.com/v1')).toBeInTheDocument();
    // 启用状态 Badge
    expect(screen.getByText('已启用')).toBeInTheDocument();
  });

  it('展示 API Key 明文（等宽字体），BR1 锚点', () => {
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    expect(screen.getByText('sk-plaintext-secret-key')).toBeInTheDocument();
  });

  it('点复制按钮触发 navigator.clipboard.writeText(api_key)，成功 toast「API Key 已复制到剪贴板」（BR1 §4.4.3）', async () => {
    writeTextMock.mockResolvedValue(undefined);
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '复制' }));
    await waitFor(() => expect(writeTextMock).toHaveBeenCalledWith('sk-plaintext-secret-key'));
    expect(toastSuccess).toHaveBeenCalledWith('API Key 已复制到剪贴板');
  });

  it('clipboard.writeText 失败时提示手动复制（BR1 §4.4.3）', async () => {
    writeTextMock.mockRejectedValue(new Error('denied'));
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '复制' }));
    await waitFor(() => expect(writeTextMock).toHaveBeenCalled());
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('复制失败，请手动复制'));
  });

  it('点编辑模型按钮：关闭查看弹窗并回调 onEdit(detail)（BR2 §4.4.3）', () => {
    const onOpenChange = vi.fn();
    const onEdit = vi.fn();
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={onOpenChange} onEdit={onEdit} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /编辑模型/ }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onEdit).toHaveBeenCalledWith(detail);
  });

  it('弹窗宽度 className 含 max-w-[600px]', () => {
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    // DialogContent 经 Portal 渲染到 document.body，按 data-slot 全局查
    const content = document.querySelector('[data-slot="dialog-content"]');
    expect(content?.className).toContain('max-w-[600px]');
  });

  it('禁用态模型渲染「未启用」Badge', () => {
    detailMock.mockReturnValue({ ...detail, enabled: false });
    render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    expect(screen.getByText('未启用')).toBeInTheDocument();
  });

  it('弹窗关闭（open=false）时 removeQueries 清掉 detail 前缀明文缓存（specs §4.4.3）', async () => {
    const { rerender } = render(
      <LLMViewDialog open llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    expect(removeQueriesMock).not.toHaveBeenCalled();
    rerender(
      <LLMViewDialog open={false} llmId={detail.id} onOpenChange={() => {}} onEdit={() => {}} />,
    );
    await waitFor(() => expect(removeQueriesMock).toHaveBeenCalled());
    const args = removeQueriesMock.mock.calls[0][0];
    expect(args).toEqual({ queryKey: ['llm-configs', 'detail'] });
  });
});
