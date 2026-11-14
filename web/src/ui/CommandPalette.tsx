import { useEffect, useId, useMemo, useRef, useState } from 'react';

// The command palette (⌘K / Ctrl+K): one box to go anywhere or do anything by
// typing a few letters of it. Every word typed must appear in a command's
// label or hint, so "web st" finds "Stop web".

export interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

export function matchCommands(commands: Command[], query: string): Command[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return commands;
  return commands.filter(c => {
    const text = `${c.label} ${c.hint ?? ''}`.toLowerCase();
    return words.every(w => text.includes(w));
  });
}

// isPaletteShortcut says whether a key press should toggle the palette: ⌘K
// on a Mac, Ctrl+K elsewhere — except inside a VM console, which passes every
// key to the guest (Ctrl+K cuts a line in nano and kills to the end of the
// line in a shell).
export function isPaletteShortcut(e: KeyboardEvent): boolean {
  if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'k') return false;
  if (e.defaultPrevented) return false;
  return !(e.target instanceof Element && e.target.closest('[data-console]'));
}

export function CommandPalette({ open, commands, onClose }: { open: boolean; commands: Command[]; onClose: () => void }) {
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listId = useId();
  const matches = useMemo(() => matchCommands(commands, query), [commands, query]);

  // Start fresh each time it opens, and give focus back to wherever it was
  // when it closes.
  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement as HTMLElement | null;
    setQuery('');
    setActive(0);
    inputRef.current?.focus();
    return () => previous?.focus?.();
  }, [open]);

  if (!open) return null;

  function run(c: Command) {
    onClose();
    c.run();
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive(i => Math.min(i + 1, Math.max(matches.length - 1, 0)));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive(i => Math.max(i - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const c = matches[active];
      if (c) run(c);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    }
  }

  const optionId = (i: number) => `${listId}-option-${i}`;

  return (
    <div
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose();
      }}
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 60,
        background: 'var(--nd-scrim)',
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'flex-start',
        paddingTop: '12vh',
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        style={{
          width: 'min(560px, calc(100% - 32px))',
          borderRadius: 14,
          overflow: 'hidden',
          background: 'var(--nd-surface)',
          border: '1px solid var(--nd-line)',
          boxShadow: 'var(--nd-shadow-lg)',
          animation: 'ncfadein 0.15s ease',
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            padding: '14px 16px',
            borderBottom: '1px solid var(--nd-line)',
          }}
        >
          <span aria-hidden="true" style={{ fontFamily: 'var(--nd-font-mono)', color: 'var(--nd-accent)' }}>
            ›
          </span>
          <input
            ref={inputRef}
            role="combobox"
            aria-label="Command"
            aria-expanded="true"
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={matches[active] ? optionId(active) : undefined}
            placeholder="Type a command or a VM name"
            value={query}
            onChange={e => {
              setQuery(e.target.value);
              setActive(0);
            }}
            onKeyDown={onKeyDown}
            style={{
              flex: 1,
              minWidth: 0,
              border: 0,
              // The open palette is itself the focus indicator; a ring round
              // its only input would just box the text in.
              boxShadow: 'none',
              background: 'transparent',
              color: 'var(--nd-text)',
              fontSize: 15,
            }}
          />
          <kbd
            style={{
              fontFamily: 'var(--nd-font-mono)',
              fontSize: 11.5,
              padding: '2px 6px',
              borderRadius: 6,
              border: '1px solid var(--nd-line)',
              color: 'var(--nd-muted)',
            }}
          >
            esc
          </kbd>
        </div>
        <ul
          id={listId}
          role="listbox"
          aria-label="Commands"
          style={{ listStyle: 'none', margin: 0, padding: 6, maxHeight: 360, overflowY: 'auto' }}
        >
          {matches.length === 0 && (
            <li role="presentation" style={{ padding: '12px', color: 'var(--nd-muted)', fontSize: 13.5 }}>
              No matching command
            </li>
          )}
          {matches.map((c, i) => (
            <li
              key={c.id}
              id={optionId(i)}
              role="option"
              aria-selected={i === active}
              onMouseEnter={() => setActive(i)}
              onMouseDown={e => e.preventDefault()}
              onClick={() => run(c)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 12,
                minHeight: 42,
                padding: '0 12px',
                borderRadius: 9,
                cursor: 'pointer',
                background: i === active ? 'var(--nd-surface-2)' : 'transparent',
                color: 'var(--nd-text)',
                fontSize: 14,
              }}
            >
              <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.label}</span>
              {c.hint && (
                <span
                  style={{
                    fontSize: 12,
                    color: 'var(--nd-muted)',
                    fontFamily: c.hint.includes('@') ? 'var(--nd-font-mono)' : undefined,
                  }}
                >
                  {c.hint}
                </span>
              )}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
