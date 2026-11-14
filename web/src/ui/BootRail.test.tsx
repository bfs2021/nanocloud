import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BootRail, railSteps } from './BootRail';

describe('railSteps', () => {
  it.each([
    { vm: { status: 'stopped', phase: 'stopped' }, want: null },
    { vm: { status: 'running', phase: 'stopping' }, want: null },
    { vm: { status: 'running', phase: 'crash-looping' }, want: null },
    { vm: { status: 'running', phase: 'booting' }, want: ['done', 'done', 'active', 'todo', 'todo'] },
    {
      vm: { status: 'running', phase: 'booting', ip: '10.0.0.5' },
      want: ['done', 'done', 'done', 'active', 'todo'],
    },
    { vm: { status: 'running', phase: 'no-network' }, want: ['done', 'done', 'failed', 'todo', 'todo'] },
    { vm: { status: 'running', phase: 'ready', ip: '10.0.0.5' }, want: ['done', 'done', 'done', 'done', 'done'] },
  ] as const)('$vm.status/$vm.phase ip=$vm.ip', ({ vm, want }) => {
    expect(railSteps(vm)).toEqual(want);
  });
});

describe('BootRail', () => {
  it('reports how far the boot has come to assistive tech', () => {
    render(<BootRail steps={['done', 'done', 'done', 'active', 'todo']} />);
    const bar = screen.getByRole('progressbar', { name: 'Boot progress' });
    expect(bar).toHaveAttribute('aria-valuenow', '3');
    expect(bar).toHaveAttribute('aria-valuemax', '5');
    expect(bar).toHaveAttribute('aria-valuetext', 'Install: in progress');
  });

  it('names the step that failed', () => {
    render(<BootRail steps={['done', 'done', 'failed', 'todo', 'todo']} />);
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuetext', 'Network: failed');
  });

  it('reads Ready once every step is done', () => {
    render(<BootRail steps={['done', 'done', 'done', 'done', 'done']} labels={false} />);
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuetext', 'Ready');
  });
});
