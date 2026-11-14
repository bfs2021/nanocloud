import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react';

export interface TabItem<T extends string> {
  id: T;
  label: ReactNode;
}

interface TabsProps<T extends string> {
  // label names the tab list for a screen reader ("VM sections").
  label: string;
  items: TabItem<T>[];
  value: T;
  onChange: (id: T) => void;
  // children is the selected tab's panel.
  children: ReactNode;
}

// Tabs follows the WAI-ARIA tabs pattern: one tab stop for the whole list,
// arrow keys and Home/End move between tabs and select as they go.
export function Tabs<T extends string>({ label, items, value, onChange, children }: TabsProps<T>) {
  const base = useId();
  const list = useRef<HTMLDivElement>(null);
  const tabId = (id: T) => `${base}-tab-${id}`;

  function onKeyDown(e: KeyboardEvent) {
    const at = items.findIndex(t => t.id === value);
    const moves: Record<string, number> = {
      ArrowRight: (at + 1) % items.length,
      ArrowLeft: (at - 1 + items.length) % items.length,
      Home: 0,
      End: items.length - 1,
    };
    const next = moves[e.key];
    if (next === undefined) return;
    e.preventDefault();
    onChange(items[next].id);
    list.current?.querySelector<HTMLElement>(`#${CSS.escape(tabId(items[next].id))}`)?.focus();
  }

  return (
    <>
      <div ref={list} role="tablist" aria-label={label} className="nd-tabs" onKeyDown={onKeyDown}>
        {items.map(t => (
          <button
            key={t.id}
            id={tabId(t.id)}
            type="button"
            role="tab"
            className="nd-tab"
            aria-selected={t.id === value}
            aria-controls={`${base}-panel`}
            tabIndex={t.id === value ? 0 : -1}
            onClick={() => onChange(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div id={`${base}-panel`} role="tabpanel" aria-labelledby={tabId(value)} tabIndex={0}>
        {children}
      </div>
    </>
  );
}
