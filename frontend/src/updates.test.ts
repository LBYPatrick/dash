import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vite-plus/test';

vi.mock('./i18n', () => ({ t: (key: string) => key }));
vi.mock('./i18n/backend', () => ({
  backendMessage: (message: string) => message,
}));
vi.mock('./indexer', () => ({ indexStatus: { value: { driving: 'parked' } } }));
const current = { version: '1', tag: 'old', channel: 'main', revision: 'a' };
const initial = () => ({
  managed: true,
  repository: 'LBYPatrick/dash',
  current,
  channel: 'main',
  automatic: true,
  phase: 'idle',
});
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });

beforeEach(() => {
  vi.resetModules();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('update task lifetime', () => {
  it('resumes startup-health polling after a tab reload', async () => {
    const reload = vi.fn();
    const removeItem = vi.fn();
    vi.stubGlobal('location', { reload });
    vi.stubGlobal('navigator', {});
    vi.stubGlobal('sessionStorage', { getItem: () => 'old', removeItem });
    const fetch = vi.fn().mockResolvedValueOnce(
      response({
        ...initial(),
        current: { ...current, tag: 'new' },
        phase: 'restarting',
        pendingHealth: true,
      }),
    );
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    await updates.refreshUpdates();
    expect(reload).not.toHaveBeenCalled();
    fetch.mockResolvedValueOnce(
      response({ ...initial(), current: { ...current, tag: 'new' } }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(reload).toHaveBeenCalledOnce();
    expect(removeItem).toHaveBeenCalledWith('dash.update.restart');
  });

  it('restores the ready state when installation is explicitly rejected', async () => {
    const ready = { ...initial(), phase: 'ready' };
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response(ready))
      .mockResolvedValueOnce(response({ error: 'not parked' }, 409));
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    await updates.refreshUpdates();
    await updates.updateAction('install', 'main', true);
    expect(updates.updateState.value?.phase).toBe('ready');
    expect(updates.updateBusy.value).toBe(false);
    expect(updates.updateError.value).toBe('not parked');
  });

  it('keeps the page on the recovered build when activation rolls back', async () => {
    const reload = vi.fn();
    vi.stubGlobal('location', { reload });
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response(initial()))
      .mockResolvedValueOnce(response({ ...initial(), phase: 'restarting' }));
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    await updates.refreshUpdates();
    await updates.updateAction('install', 'main', true);
    fetch.mockResolvedValueOnce(
      response({
        ...initial(),
        phase: 'error',
        message: 'Startup check failed; previous release restored.',
      }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(reload).not.toHaveBeenCalled();
    expect(updates.updateState.value?.phase).toBe('error');
    expect(updates.updateBusy.value).toBe(false);
  });

  it('clears a connection error after a successful retry', async () => {
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue(response(initial()));
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    await updates.refreshUpdates();
    expect(updates.updateError.value).toBe('offline');
    await updates.refreshUpdates();
    expect(updates.updateError.value).toBe('');
    expect(updates.updateState.value?.phase).toBe('idle');
  });

  it('does not let an older poll revert saved preferences', async () => {
    let finish!: (result: Response) => void;
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response(initial()))
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValueOnce(
        response({ ...initial(), channel: 'nightly', automatic: false }),
      );
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    await updates.refreshUpdates();
    const oldPoll = updates.refreshUpdates();
    await updates.updateAction('configure', 'nightly', false);
    finish(response(initial()));
    await oldPoll;
    expect(updates.updateState.value?.channel).toBe('nightly');
    expect(updates.updateState.value?.automatic).toBe(false);
  });

  it('keeps reconnecting after the sheet closes and waits for startup health', async () => {
    const reload = vi.fn();
    vi.stubGlobal('location', { reload });
    vi.stubGlobal('navigator', {});
    const fetch = vi.fn().mockResolvedValueOnce(response(initial()));
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    const close = updates.observeUpdates();
    await updates.refreshUpdates();
    fetch.mockResolvedValueOnce(
      response({ ...initial(), phase: 'restarting', pendingHealth: true }),
    );
    await updates.updateAction('install', 'main', true);
    close();
    fetch.mockResolvedValueOnce(
      response({
        ...initial(),
        current: { ...current, tag: 'new' },
        phase: 'restarting',
        pendingHealth: true,
      }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(reload).not.toHaveBeenCalled();
    fetch.mockResolvedValueOnce(
      response({ ...initial(), current: { ...current, tag: 'new' } }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(reload).toHaveBeenCalledOnce();
  });

  it('recovers if an accepted installation loses its HTTP response', async () => {
    const reload = vi.fn();
    vi.stubGlobal('location', { reload });
    vi.stubGlobal('navigator', {});
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response(initial()))
      .mockRejectedValueOnce(new TypeError('network closed'));
    vi.stubGlobal('fetch', fetch);
    const updates = await import('./updates');
    await updates.refreshUpdates();
    await updates.updateAction('install', 'main', true);
    expect(updates.updateError.value).toBe('');
    fetch.mockResolvedValueOnce(
      response({ ...initial(), current: { ...current, tag: 'new' } }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(reload).toHaveBeenCalledOnce();
  });
});
