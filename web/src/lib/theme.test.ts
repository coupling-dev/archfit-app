import { describe, expect, it } from 'vitest';
import { applyTheme, saveTheme, storedTheme } from './theme';

function memory(): Storage {
  const m = new Map<string, string>();
  return {
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
    clear: () => m.clear(),
    key: () => null,
    get length() {
      return m.size;
    },
  };
}

const broken = {
  getItem: () => {
    throw new Error('blocked');
  },
  setItem: () => {
    throw new Error('blocked');
  },
  removeItem: () => {
    throw new Error('blocked');
  },
};

describe('theme', () => {
  it('remembers light and dark, and auto clears the choice', () => {
    const s = memory();
    expect(storedTheme(s)).toBe('auto');
    saveTheme('dark', s);
    expect(storedTheme(s)).toBe('dark');
    saveTheme('auto', s);
    expect(storedTheme(s)).toBe('auto');
    expect(s.length).toBe(0);
  });

  it('reads an unknown stored value as auto', () => {
    const s = memory();
    s.setItem('archfit-theme', 'sepia');
    expect(storedTheme(s)).toBe('auto');
  });

  it('works without storage', () => {
    expect(storedTheme(broken)).toBe('auto');
    expect(() => saveTheme('dark', broken)).not.toThrow();
    expect(storedTheme(undefined)).toBe('auto');
  });

  it('sets data-theme only for an explicit choice', () => {
    const root = { dataset: {} as DOMStringMap } as HTMLElement;
    applyTheme('dark', root);
    expect(root.dataset.theme).toBe('dark');
    applyTheme('auto', root);
    expect(root.dataset.theme).toBeUndefined();
  });
});
