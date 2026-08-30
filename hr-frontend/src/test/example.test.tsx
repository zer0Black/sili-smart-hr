import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Button } from '@/components/ui/button';

describe('vitest 基建自检', () => {
  it('渲染 Button 并断言文本', () => {
    render(<Button>确认</Button>);
    expect(screen.getByRole('button', { name: '确认' })).toBeInTheDocument();
  });
});
