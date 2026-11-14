import type { HTMLAttributes } from 'react';

// Card is the raised panel content sits on; the same surface as design.ts's
// surfaceCard, for code that has moved off inline styles.
export function Card({ as: Tag = 'section', className, ...rest }: HTMLAttributes<HTMLElement> & { as?: 'section' | 'div' }) {
  return <Tag className={['nd-card', className].filter(Boolean).join(' ')} {...rest} />;
}
