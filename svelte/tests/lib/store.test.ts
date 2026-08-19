import { describe, it, expect, vi } from 'vitest';
import { get } from 'svelte/store';
import { toasts, addToast, removeToast } from '../../src/lib/store';

describe('toast store', () => {
  it('adds a toast and auto-removes after timeout', () => {
    vi.useFakeTimers();
    addToast('hello', 'info');
    let list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0].message).toBe('hello');
    expect(list[0].type).toBe('info');

    vi.advanceTimersByTime(4001);
    list = get(toasts);
    expect(list).toHaveLength(0);
    vi.useRealTimers();
  });

  it('removes a toast by id', () => {
    addToast('keep');
    addToast('remove');
    const list = get(toasts);
    const target = list.find((t) => t.message === 'remove');
    expect(target).toBeDefined();

    removeToast(target!.id);
    expect(get(toasts).some((t) => t.id === target!.id)).toBe(false);
  });
});
