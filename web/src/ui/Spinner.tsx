// Spinner is the spinning ring shown beside a "…ing" label.
export function Spinner({
  size,
  track = 'var(--nd-line)',
  head = 'var(--nd-accent)',
  shrink,
}: {
  size: number;
  track?: string;
  head?: string;
  // shrink pins the ring's size in a flex row whose text may wrap.
  shrink?: boolean;
}) {
  return (
    <span
      style={{
        width: size,
        height: size,
        border: `2px solid ${track}`,
        borderTopColor: head,
        borderRadius: '50%',
        display: 'inline-block',
        animation: 'ncspin .7s linear infinite',
        flexShrink: shrink ? 0 : undefined,
      }}
    />
  );
}
