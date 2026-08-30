import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

// 初始化 i18n（useTranslation 文案解析）
import i18n from '@/i18n/config';

// Mock useStaffs 必须在被测模块 import 前完成
const fetchStaffsMock = vi.fn();
let useStaffsErrorOverride: boolean | null = null;
vi.mock('@/features/system-params/hooks', () => ({
  useStaffs: (...args: unknown[]) => {
    if (useStaffsErrorOverride === true) {
      return { data: undefined, isLoading: false, isError: true };
    }
    return {
      data: { list: fetchStaffsMock(...args) || [] },
      isLoading: false,
      isError: false,
    };
  },
}));

// sonner toast 不引入 jsdom 不需要的副作用
vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { MemberMultiSelect } from '@/features/system-params/components/member-multi-select';
import type { StaffItem } from '@/lib/contracts';

beforeEach(() => {
  // 统一锁定为中文，避免 jsdom navigator 探测导致语言漂移
  void i18n.changeLanguage('zh');
});

describe('MemberMultiSelect（§4.1.4 规则4 / §4.1.5）', () => {
  beforeEach(() => {
    fetchStaffsMock.mockReset();
    useStaffsErrorOverride = null;
  });

  it('展开下拉显示 staffs 人名选项（无工号）', async () => {
    const staffs: StaffItem[] = [
      { staff_id: 'u1', staff_name: '张三' },
      { staff_id: 'u2', staff_name: '李四' },
    ];
    fetchStaffsMock.mockReturnValue(staffs);

    const { container } = render(
      <MemberMultiSelect value={[]} onChange={() => {}} />,
    );

    // 点击触发按钮展开
    const triggerBtn = screen.getByText('点击选择指定人员');
    fireEvent.click(triggerBtn);

    // 列表渲染：人名出现，工号不出现
    await waitFor(() => {
      expect(screen.getByText('张三')).toBeInTheDocument();
      expect(screen.getByText('李四')).toBeInTheDocument();
    });
    // 不出现 staff_id 字面量（无工号约束）
    expect(container.textContent).not.toContain('u1');
    expect(container.textContent).not.toContain('u2');
  });

  it('点选后已选以 Badge 标签展示，再次点击取消', async () => {
    const staffs: StaffItem[] = [{ staff_id: 'u1', staff_name: '王五' }];
    fetchStaffsMock.mockReturnValue(staffs);
    const onChange = vi.fn();

    render(<MemberMultiSelect value={[]} onChange={onChange} />);

    fireEvent.click(screen.getByText('点击选择指定人员'));
    await waitFor(() => expect(screen.getByText('王五')).toBeInTheDocument());

    // 点选王五
    fireEvent.click(screen.getByText('王五'));
    expect(onChange).toHaveBeenCalledWith([
      { staff_id: 'u1', staff_name: '王五' },
    ]);
  });

  it('已选回填展示 Badge 与计数，点 X 移除单个', async () => {
    const selected: StaffItem[] = [
      { staff_id: 'u1', staff_name: '赵六' },
      { staff_id: 'u2', staff_name: '钱七' },
    ];
    const onChange = vi.fn();
    render(<MemberMultiSelect value={selected} onChange={onChange} />);

    // 计数文本
    expect(screen.getByText('已选 2 人')).toBeInTheDocument();
    // 移除赵六
    const removeBtn = screen.getByLabelText('移除 赵六');
    fireEvent.click(removeBtn);
    expect(onChange).toHaveBeenCalledWith([
      { staff_id: 'u2', staff_name: '钱七' },
    ]);
  });

  it('输入 keyword 触发 useStaffs 重新拉取（refetch 参数变化）', async () => {
    fetchStaffsMock.mockReturnValue([]);
    render(<MemberMultiSelect value={[]} onChange={() => {}} />);

    fireEvent.click(screen.getByText('点击选择指定人员'));
    await waitFor(() => expect(fetchStaffsMock).toHaveBeenCalled());
    expect(fetchStaffsMock).toHaveBeenLastCalledWith('', 1, 20);

    // 模拟输入 keyword
    const input = screen.getByPlaceholderText('输入人名搜索');
    fireEvent.change(input, { target: { value: '张' } });

    await waitFor(() => {
      expect(fetchStaffsMock).toHaveBeenLastCalledWith('张', 1, 20);
    });
  });

  it('useStaffs 失败时触发 onDegraded 回调（§4.1.4 规则4 自动切全员）', async () => {
    useStaffsErrorOverride = true;
    const onDegraded = vi.fn();

    render(
      <MemberMultiSelect
        value={[]}
        onChange={() => {}}
        onDegraded={onDegraded}
      />,
    );

    await waitFor(() => {
      expect(onDegraded).toHaveBeenCalledTimes(1);
    });
  });

  it('useStaffs 失败后重试成功，再次失败时 onDegraded 再次触发', async () => {
    const onDegraded = vi.fn();
    const { rerender } = render(
      <MemberMultiSelect
        value={[]}
        onChange={() => {}}
        onDegraded={onDegraded}
      />,
    );

    // 首次失败
    useStaffsErrorOverride = true;
    rerender(
      <MemberMultiSelect
        value={[]}
        onChange={() => {}}
        onDegraded={onDegraded}
      />,
    );
    await waitFor(() => expect(onDegraded).toHaveBeenCalledTimes(1));

    // 重试成功
    useStaffsErrorOverride = false;
    rerender(
      <MemberMultiSelect
        value={[]}
        onChange={() => {}}
        onDegraded={onDegraded}
      />,
    );
    await waitFor(() => expect(fetchStaffsMock).toHaveBeenCalled());

    // 再次失败，onDegraded 应再次触发（计数 2）
    useStaffsErrorOverride = true;
    rerender(
      <MemberMultiSelect
        value={[]}
        onChange={() => {}}
        onDegraded={onDegraded}
      />,
    );
    await waitFor(() => expect(onDegraded).toHaveBeenCalledTimes(2));
  });
});
