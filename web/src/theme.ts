import { useCallback, useEffect, useState } from 'react';

// The UI has two themes, set as data-theme on <html> (see index.css): dark,
// the default, and light. The choice is a per-browser convenience, so it lives
// in localStorage — which can be unavailable (private windows, blocked site
// data), in which case the default simply applies.

export type Theme = 'dark' | 'light';
// What the person picked: a theme, or whatever the operating system uses.
export type ThemeChoice = Theme | 'system';

const STORAGE_KEY = 'nanocloud.theme';
const CHANGE_EVENT = 'nanocloud:theme';
const LIGHT_QUERY = '(prefers-color-scheme: light)';

// A choice made while storage refuses writes still holds for this visit.
let unsaved: ThemeChoice | null = null;

export function storedChoice(): ThemeChoice {
  if (unsaved) return unsaved;
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return v === 'light' || v === 'system' ? v : 'dark';
  } catch {
    return 'dark';
  }
}

function systemTheme(): Theme {
  return typeof window.matchMedia === 'function' && window.matchMedia(LIGHT_QUERY).matches ? 'light' : 'dark';
}

export function storedTheme(): Theme {
  const c = storedChoice();
  return c === 'system' ? systemTheme() : c;
}

export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', theme === 'light' ? '#f6f7fb' : '#0a0e17');
}

export function chooseTheme(choice: ThemeChoice) {
  unsaved = null;
  try {
    localStorage.setItem(STORAGE_KEY, choice);
  } catch {
    unsaved = choice;
  }
  applyTheme(storedTheme());
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

// followSystemTheme keeps a "system" choice in step with the OS switching
// between light and dark while the page is open.
export function followSystemTheme() {
  if (typeof window.matchMedia !== 'function') return;
  window.matchMedia(LIGHT_QUERY).addEventListener('change', () => {
    if (storedChoice() !== 'system') return;
    applyTheme(systemTheme());
    window.dispatchEvent(new Event(CHANGE_EVENT));
  });
}

function useThemeEvent<T>(read: () => T): T {
  const [value, setValue] = useState(read);
  useEffect(() => {
    const onChange = () => setValue(read());
    window.addEventListener(CHANGE_EVENT, onChange);
    return () => window.removeEventListener(CHANGE_EVENT, onChange);
  }, [read]);
  return value;
}

const appliedTheme = (): Theme => (document.documentElement.dataset.theme === 'light' ? 'light' : 'dark');

// useTheme is the theme on screen and a switch to the other one. Every
// instance follows a change made anywhere: the sidebar and Settings agree.
export function useTheme(): [Theme, () => void] {
  const theme = useThemeEvent(appliedTheme);
  const toggle = useCallback(() => chooseTheme(appliedTheme() === 'light' ? 'dark' : 'light'), []);
  return [theme, toggle];
}

export function useThemeChoice(): [ThemeChoice, (c: ThemeChoice) => void] {
  return [useThemeEvent(storedChoice), chooseTheme];
}
