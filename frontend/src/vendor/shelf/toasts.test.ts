import { describe, it, expect } from 'vitest';
import { useToasts } from './toasts';
describe('Shelf notifications', () => {
  it('stacks notices and replaces a matching ID without losing actions', () => {
    const { show, toasts, dismiss, clear } = useToasts();
    clear();
    const first = show({ message: 'Download ready', tone: 'success' });
    show({
      id: 'connection',
      message: 'Connecting',
      tone: 'info',
      expire: undefined,
    });
    const action = { label: 'Retry', run: () => {} };
    show({
      id: 'connection',
      message: 'Connection lost',
      tone: 'error',
      action,
    });
    expect(toasts.value).toHaveLength(2);
    expect(toasts.value[0].expire).toBe(5000);
    expect(toasts.value[1].message).toBe('Connection lost');
    expect(toasts.value[1].action).toEqual(action);
    dismiss(first);
    expect(toasts.value.map((n) => n.id)).toEqual(['connection']);
    clear();
    expect(toasts.value).toHaveLength(0);
  });
});
