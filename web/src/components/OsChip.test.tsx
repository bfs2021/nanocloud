import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { OsChip } from './OsChip';
import { OS_LOGOS } from '../osLogos';

const drawn = (name: string) => screen.getByRole('img', { name }).querySelector('path')?.getAttribute('d');

describe('OsChip', () => {
  it('names the distribution and draws its own logo', () => {
    render(<OsChip os="Debian" />);
    expect(drawn('Debian')).toBe(OS_LOGOS.debian);
  });

  it('falls back to the Linux penguin for a distribution it does not know', () => {
    render(<OsChip os="gentoo" />);
    expect(drawn('gentoo')).toBe(OS_LOGOS.linux);
  });
});
