import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

// 初始化 i18n（useTranslation 文案解析）
import i18n from '@/i18n/config';

// Mock useStaffs 必须在被测模块 import 前完成
const useStaffsMock = vi.hoisted(() => vi.fn());
vi.mock('@/features/system-params/hooks', () => ({
  useStaffs: (...args: unknown[]) => useStaffsMock(...args),
}));

import { StaffMultiSelect } from '@/features/assessment/components/staff-multi-select';
import type { StaffSelectValue } from '@/features/assessment/components/staff-multi-select';
import type { StaffItem } from '@/lib/contracts';

const STAFF_A: StaffItem = { staff_id: 'u1', staff_name: '张三' };
const STAFF_B: StaffItem = { staff_id: 'u2', staff_name: '李四' };

function okResult(list: StaffItem[], total?: number, page = 1) {
  return {
    data: { list, total: total ?? list.length, page, page_size: 20 },
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  };
}

function renderSelect(
  value: StaffSelectValue = { mode: 'specified', staffs: [] },
  onChange = vi.fn(),
) {
  render(<StaffMultiSelect value={value} onChange={onChange} />);
  return onChange;
}

beforeEach(() => {
  void i18n.changeLanguage('zh');
  useStaffsMock.mockReset();
  useStaffsMock.mockReturnValue(okResult([STAFF_A, STAFF_B]));
});

describe('StaffMultiSelect（specs §4.2.2/§4.2.3/§4.2.4 规则1）', () => {
  it('TestStaffSelectAllMutex：点全员收到 {mode:all, staffs:[]}；全员态点具体人切 specified', async () => {
    const onChange = renderSelect({ mode: 'specified', staffs: [STAFF_A] });

    fireEvent.click(screen.getByText('点击选择评估对象'));
    fireEvent.click(await screen.findByRole('button', { name: /全员/ }));
    expect(onChange).toHaveBeenLastCalledWith({ mode: 'all', staffs: [] });

    // 全员态下点具体人员：移除全员，只保留该人
    const onChange2 = vi.fn();
    render(<StaffMultiSelect value={{ mode: 'all', staffs: [] }} onChange={onChange2} />);
    const allButtons = screen.getAllByText('点击选择评估对象');
    fireEvent.click(allButtons[allButtons.length - 1]);
    const options = await screen.findAllByRole('button', { name: /张三/ });
    fireEvent.click(options[options.length - 1]);
    expect(onChange2).toHaveBeenLastCalledWith({ mode: 'specified', staffs: [STAFF_A] });
  });

  it('TestStaffSelectErrorRetry：接口失败展示错误态与重试，组件未置灰', async () => {
    const refetch = vi.fn();
    useStaffsMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      refetch,
    });
    const onChange = renderSelect();

    fireEvent.click(screen.getByText('点击选择评估对象'));
    expect(await screen.findByText('人员加载失败')).toBeInTheDocument();

    const retryBtn = screen.getByRole('button', { name: '重试' });
    fireEvent.click(retryBtn);
    expect(refetch).toHaveBeenCalledTimes(1);

    // 组件未置灰：触发按钮仍可点击，onChange 未被强制切全员
    expect(screen.getByText('点击选择评估对象').closest('button')).not.toBeDisabled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('TestStaffSelectMergeSelected：翻页后已选项仍在渲染列表（合并去重）', async () => {
    const selected: StaffItem[] = [STAFF_A, STAFF_B];
    useStaffsMock.mockImplementation((keyword: string, page: number) => {
      if (keyword === '' && page === 1) {
        return okResult(
          [
            { staff_id: 'u1', staff_name: '张三' },
            { staff_id: 'u3', staff_name: '王五' },
          ],
          24,
          1,
        );
      }
      if (keyword === '' && page === 2) {
        return okResult(
          [
            { staff_id: 'u2', staff_name: '李四' },
            { staff_id: 'u4', staff_name: '赵六' },
          ],
          24,
          2,
        );
      }
      return okResult([], 0);
    });

    renderSelect({ mode: 'specified', staffs: selected });
    fireEvent.click(screen.getByText('点击选择评估对象'));

    // 已选 u1 合并进列表（带选中态），当页 u3 也在，出现分页条
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /王五/ })).toBeInTheDocument(),
    );
    expect(screen.getByRole('button', { name: /^张三$/ })).toBeInTheDocument();
    expect(screen.getByLabelText('移除 张三')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '下一页' })).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole('button', { name: '下一页' }));
    await waitFor(() =>
      expect(useStaffsMock).toHaveBeenLastCalledWith('', 2, 20),
    );

    // 第 2 页：已选 u1 仍在列表；u2 同为已选与当页项去重只出现一次；u4 入列表
    expect(screen.getByRole('button', { name: /^张三$/ })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /^李四$/ })).toHaveLength(1);
    expect(screen.getByRole('button', { name: /赵六/ })).toBeInTheDocument();
    expect(screen.getByLabelText('移除 李四')).toBeInTheDocument();
  });

  it('已选人员 Badge 平铺展示，点 X 单独移除', async () => {
    const onChange = renderSelect({
      mode: 'specified',
      staffs: [STAFF_A, STAFF_B],
    });
    expect(screen.getByLabelText('移除 张三')).toBeInTheDocument();
    expect(screen.getByLabelText('移除 李四')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('移除 张三'));
    expect(onChange).toHaveBeenLastCalledWith({
      mode: 'specified',
      staffs: [STAFF_B],
    });
  });

  it('specified 态下再次点击已选人员取消选中', async () => {
    useStaffsMock.mockReturnValue(okResult([STAFF_A, STAFF_B]));
    const onChange = renderSelect({ mode: 'specified', staffs: [STAFF_A] });
    fireEvent.click(screen.getByText('点击选择评估对象'));
    // 已选 u1 合并进列表带选中态，再点即取消
    fireEvent.click(await screen.findByRole('button', { name: /^张三$/ }));
    expect(onChange).toHaveBeenLastCalledWith({ mode: 'specified', staffs: [] });
  });

  it('弹窗打开即加载首屏：展开时 useStaffs 以空 keyword 第 1 页调用', async () => {
    renderSelect();
    fireEvent.click(screen.getByText('点击选择评估对象'));
    await waitFor(() =>
      expect(useStaffsMock).toHaveBeenCalledWith('', 1, 20),
    );
  });

  it('输入 keyword 防抖后触发搜索', async () => {
    renderSelect();
    fireEvent.click(screen.getByText('点击选择评估对象'));
    await waitFor(() => expect(useStaffsMock).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('输入人名搜索'), {
      target: { value: '张' },
    });
    await waitFor(() =>
      expect(useStaffsMock).toHaveBeenLastCalledWith('张', 1, 20),
    );
  });
});
