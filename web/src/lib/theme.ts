// Light, dark, or the system's choice. Kept per browser in localStorage; storage can be unavailable (private mode,
// blocked site data), and then the choice lasts for the page only.

export type Theme = 'auto' | 'light' | 'dark';

export const themes: Theme[] = ['auto', 'light', 'dark'];

const key = 'archfit-theme';

export function storedTheme(storage: Pick<Storage, 'getItem'> | undefined = safeStorage()): Theme {
  try {
    const t = storage?.getItem(key);
    return t === 'light' || t === 'dark' ? t : 'auto';
  } catch {
    return 'auto';
  }
}

export function saveTheme(t: Theme, storage: Pick<Storage, 'setItem' | 'removeItem'> | undefined = safeStorage()) {
  try {
    if (t === 'auto') storage?.removeItem(key);
    else storage?.setItem(key, t);
  } catch {
    // the choice still applies to this page
  }
}

// applyTheme sets data-theme on <html>; app.css reads it, and "auto" follows prefers-color-scheme.
export function applyTheme(t: Theme, root: HTMLElement = document.documentElement) {
  if (t === 'auto') delete root.dataset.theme;
  else root.dataset.theme = t;
}

function safeStorage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}
