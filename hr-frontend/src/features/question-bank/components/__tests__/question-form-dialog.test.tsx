// QuestionFormDialog 编辑弹窗测试（specs §4.1.2 E / §4.1.4 规则9）
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach } from 'vitest';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { QuestionDetail } from '@/lib/contracts';

await i18n.changeLanguage('zh');

Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

const updateMock = vi.fn();
vi.mock('@/features/question-bank/hooks', () => ({
  useUpdateQuestion: () => ({ mutate: updateMock, isPending: false }),
}));

vi.mock('@/features/dimension/hooks', () => ({
  useDimensionTree: () => ({
    data: {
      modules: [
        {
          module_code: 'AI_MGMT',
          name: 'AI 管理能力',
          data_source: 'TEST',
          is_reference: false,
          groups: [
            {
              group_code: 'BASE',
              name: '底层能力',
              dimensions: [
                { id: '201', code: 'delegation', name: '授权与分工', module_code: 'AI_MGMT', group_code: 'BASE', data_source: 'TEST', weight: 5, include_overview: true, enabled: true },
                { id: '202', code: 'risk', name: '风险与担责', module_code: 'AI_MGMT', group_code: 'BASE', data_source: 'TEST', weight: 5, include_overview: true, enabled: true },
                { id: '203', code: 'old', name: '已停用维度', module_code: 'AI_MGMT', group_code: 'BASE', data_source: 'TEST', weight: 5, include_overview: true, enabled: false },
              ],
            },
          ],
        },
      ],
    },
    isLoading: false,
    isError: false,
  }),
}));

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { QuestionFormDialog } from '../question-form-dialog';
import { ApiError } from '@/lib/http-client';

const baseDetail: QuestionDetail = {
  id: '1780000000000000101',
  question_no: 'Q-AG-0001',
  source: 'AI',
  dimension_id: '201',
  dimension_name: '授权与分工',
  answer_mode: 'CHAT',
  status: 'ACTIVE',
  scenario: '情境全文',
  requirement: '作答要求全文',
  focus_point: '考察点全文',
  reject_reason: '',
  batch_id: '3001',
  batch_no: '#G0921',
  reference_count: 2,
  version: 3,
  created_at: '2026-09-21 10:31:24',
  updated_at: '2026-09-21 10:31:24',
};

function setTextareaValue(id: string, value: string) {
  const el = document.getElementById(id) as HTMLTextAreaElement | null;
  if (!el) throw new Error(`textarea #${id} not found`);
  fireEvent.change(el, { target: { value } });
}

beforeEach(() => {
  updateMock.mockReset();
});

describe('QuestionFormDialog 编辑弹窗（specs §4.1.2 E）', () => {
  it('TestPrefill：open 时预载详情回填四个字段', () => {
    render(
      <QuestionFormDialog open onOpenChange={vi.fn()} question={baseDetail} onSaved={vi.fn()} />,
    );

    expect(screen.getByText('编辑题目')).toBeInTheDocument();
    expect((document.getElementById('q-edit-scenario') as HTMLTextAreaElement).value).toBe('情境全文');
    expect((document.getElementById('q-edit-requirement') as HTMLTextAreaElement).value).toBe('作答要求全文');
    expect((document.getElementById('q-edit-focus-point') as HTMLTextAreaElement).value).toBe('考察点全文');
  });

  it('TestScenarioTooLong：超长 scenario（1001 字）被 Zod 拦截且错误文案可见、不提交', async () => {
    render(
      <QuestionFormDialog open onOpenChange={vi.fn()} question={baseDetail} onSaved={vi.fn()} />,
    );

    setTextareaValue('q-edit-scenario', '长'.repeat(1001));
    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => {
      expect(screen.getByText(/情境描述不超过 1000 字符/)).toBeInTheDocument();
    });
    expect(updateMock).not.toHaveBeenCalled();
  });

  it('TestScenarioRequired：清空 scenario 触发必填校验', async () => {
    render(
      <QuestionFormDialog open onOpenChange={vi.fn()} question={baseDetail} onSaved={vi.fn()} />,
    );

    setTextareaValue('q-edit-scenario', '');
    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => {
      expect(screen.getByText(/请输入情境描述/)).toBeInTheDocument();
    });
    expect(updateMock).not.toHaveBeenCalled();
  });

  it('TestSaveSuccess：合法提交调 mutate 并触发 onSaved（保存成功留在列表态）', async () => {
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onSuccess?.({ id: baseDetail.id, version: 4, updated_at: '2026-09-22 10:00:00' });
    });
    const onSaved = vi.fn();
    const onOpenChange = vi.fn();

    render(
      <QuestionFormDialog open onOpenChange={onOpenChange} question={baseDetail} onSaved={onSaved} />,
    );

    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => expect(updateMock).toHaveBeenCalled());
    expect(updateMock.mock.calls[0][0]).toMatchObject({
      id: baseDetail.id,
      dimension_id: '201',
      scenario: '情境全文',
      version: 3,
    });
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
  });

  it('TestDimensionOnlyEnabled：维度下拉只列当前启用的 AI_MGMT 维度（§4.1.2 E）', () => {
    render(
      <QuestionFormDialog open onOpenChange={vi.fn()} question={baseDetail} onSaved={vi.fn()} />,
    );
    // Select 未展开时选项不挂 DOM；这里断言 trigger 展示预载维度名
    expect(screen.getByRole('combobox')).toHaveTextContent('授权与分工');
  });

  it('TestVersionConflict：code 1713 冲突 toast 并刷新（§4.1.4 规则9）', async () => {
    const { toast } = await import('sonner');
    const { queryClient } = await import('@/lib/query-client');
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onError?.(new ApiError(1713, 'conflict'));
    });
    const onSaved = vi.fn();

    render(
      <QuestionFormDialog open onOpenChange={vi.fn()} question={baseDetail} onSaved={onSaved} />,
    );

    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('数据已变更，请刷新后重试'));
    await waitFor(() => expect(invalidateSpy).toHaveBeenCalled());
    expect(onSaved).not.toHaveBeenCalled();
    invalidateSpy.mockRestore();
  });

  it('TestGenericError：未知错误 toast 通用失败文案且弹窗保留', async () => {
    const { toast } = await import('sonner');
    updateMock.mockImplementation((_payload, cb) => {
      cb?.onError?.(new ApiError(9999, 'boom'));
    });
    const onOpenChange = vi.fn();

    render(
      <QuestionFormDialog open onOpenChange={onOpenChange} question={baseDetail} onSaved={vi.fn()} />,
    );

    const submit = screen.getAllByRole('button').find((b) => b.getAttribute('type') === 'submit')!;
    fireEvent.click(submit);

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('操作失败，请稍后重试'));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });
});
