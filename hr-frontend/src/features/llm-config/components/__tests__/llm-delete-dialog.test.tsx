// LLMDeleteDialog 二次确认测试（specs §4.2.5 / BR3 / BR4）
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

import i18n from '@/i18n/config';

// Radix AlertDialog 在 jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView，补桩
const origHasPointerCapture = Element.prototype.hasPointerCapture;
const origReleasePointerCapture = Element.prototype.releasePointerCapture;
const origScrollIntoView = Element.prototype.scrollIntoView;

const deleteMock = vi.fn();
vi.mock('@/features/llm-config/hooks', () => ({
  useDeleteLLMConfig: () => ({
    mutate: (...args: unknown[]) => deleteMock(...args),
    isPending: false,
  }),
  useLLMConfigs: () => ({
    data: [
      { id: '1780000000000000101', name: '主力模型' },
      { id: '1780000000000000102', name: '备用模型' },
    ],
    isLoading: false,
    isError: false,
  }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import { LLMDeleteDialog } from '@/features/llm-config/components/llm-delete-dialog';
import type { LLMConfigItem } from '@/lib/contracts';

beforeEach(() => {
  void i18n.changeLanguage('zh');
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
});

afterEach(() => {
  Element.prototype.hasPointerCapture = origHasPointerCapture;
  Element.prototype.releasePointerCapture = origReleasePointerCapture;
  Element.prototype.scrollIntoView = origScrollIntoView;
});

const llm: LLMConfigItem = {
  id: '1780000000000000101',
  name: '主力模型',
  provider: 'deepseek',
  model_id: 'deepseek-chat',
  api_url: 'https://api.deepseek.com/v1',
  api_key_masked: 'sk-1***ab12',
  enabled: true,
  version: 1,
  created_at: '2026-08-12T10:00:00Z',
  updated_at: '2026-08-12T10:00:00Z',
};

describe('LLMDeleteDialog（§4.2.5 / BR3 / BR4）', () => {
  beforeEach(() => {
    deleteMock.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it('渲染确认文案含模型名称（BR3 §4.2.5）', () => {
    render(
      <LLMDeleteDialog open llm={llm} onOpenChange={() => {}} />,
    );
    expect(
      screen.getByText('删除后无法恢复，确认删除模型「主力模型」？'),
    ).toBeInTheDocument();
  });

  it('确认按钮为 destructive variant（红色，BR3 §4.2.5）', () => {
    render(
      <LLMDeleteDialog open llm={llm} onOpenChange={() => {}} />,
    );
    const confirmBtn = screen.getByRole('button', { name: '确认删除' });
    // AlertDialogAction 把 variant 透传给 Button，destructive 映射出 bg-destructive 工具类
    expect(confirmBtn.className).toContain('bg-destructive');
  });

  it('点确认按钮触发 deleteLLMConfig mutate（BR3/BR4）', async () => {
    render(
      <LLMDeleteDialog open llm={llm} onOpenChange={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() => expect(deleteMock).toHaveBeenCalled());
    // mutate 第一个参数为被删除 id
    expect(deleteMock.mock.calls[0][0]).toBe(llm.id);
  });

  it('删除成功且返回 transferred_enabled_id 时 toast 转启模型名（BR4 §4.2.4 规则3）', async () => {
    deleteMock.mockImplementation((_id: string, cb: any) => {
      cb?.onSuccess?.({ id: llm.id, transferred_enabled_id: '1780000000000000102' });
    });
    const onOpenChange = vi.fn();
    render(
      <LLMDeleteDialog open llm={llm} onOpenChange={onOpenChange} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('已自动启用模型「备用模型」'),
    );
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('删除成功但无转启（transferred_enabled_id=null）时仅 toast 已删除', async () => {
    deleteMock.mockImplementation((_id: string, cb: any) => {
      cb?.onSuccess?.({ id: llm.id, transferred_enabled_id: null });
    });
    render(
      <LLMDeleteDialog open llm={llm} onOpenChange={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('模型已删除'));
  });

  it('删除失败时弹窗保留，toast errGeneric', async () => {
    deleteMock.mockImplementation((_id: string, cb: any) => {
      cb?.onError?.(new Error('fail'));
    });
    const onOpenChange = vi.fn();
    render(
      <LLMDeleteDialog open llm={llm} onOpenChange={onOpenChange} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('删除失败，请稍后重试'));
    expect(onOpenChange).not.toHaveBeenCalled();
  });
});
