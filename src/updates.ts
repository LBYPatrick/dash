import { computed, ref } from 'vue';
import { t } from './i18n';
import { backendMessage } from './i18n/backend';
import { indexStatus } from './indexer';

interface Build {
  version: string;
  channel: string;
  revision: string;
  tag: string;
}
export interface UpdateState {
  repository: string;
  pendingHealth?: boolean;
  managed: boolean;
  phase: string;
  current: Build;
  channel: string;
  automatic: boolean;
  release?: Build;
  transferred?: number;
  total?: number;
  message?: string;
}

// The task belongs to the app, not the sheet. Dismissing Settings must not
// abandon startup-health polling or leave the PWA on the previous build.
export const updateState = ref<UpdateState>();
export const updateError = ref('');
export const updateSubmitting = ref(false);
export const updateBusy = computed(
  () =>
    updateSubmitting.value ||
    ['checking', 'downloading', 'verifying', 'restarting'].includes(
      updateState.value?.phase || '',
    ),
);
let listeners = 0;
const restartKey = 'dash.update.restart';
let restarting = '';
try {
  restarting = sessionStorage.getItem(restartKey)?.slice(0, 256) || '';
} catch {
  // Session storage may be unavailable in a private browser.
}
function rememberRestart(tag: string) {
  restarting = tag;
  try {
    if (tag) sessionStorage.setItem(restartKey, tag);
    else sessionStorage.removeItem(restartKey);
  } catch {
    // The open page still follows the in-memory task.
  }
}
let timer: ReturnType<typeof setTimeout> | undefined;
let request: Promise<void> | undefined;
let generation = 0;

function schedule() {
  clearTimeout(timer);
  if (listeners || restarting) timer = setTimeout(refreshUpdates, 2000);
}

export async function refreshUpdates(): Promise<void> {
  if (request) return request;
  const version = generation;
  request = (async () => {
    try {
      const response = await fetch('/api/updates', {
        cache: 'no-store',
        signal: AbortSignal.timeout(8000),
      });
      if (!response.ok)
        throw new Error(t('updates.updateInformationIsUnavailable'));
      const current: UpdateState = await response.json();
      // A GET begun before a preference/action POST cannot overwrite its result.
      if (version !== generation || updateSubmitting.value) return;
      updateState.value = current;
      updateError.value = '';
      if (
        restarting &&
        !current.pendingHealth &&
        current.current.tag !== restarting
      ) {
        rememberRestart('');
        try {
          const registrations =
            await navigator.serviceWorker?.getRegistrations();
          await Promise.all(
            (registrations || []).map((r) => r.update().catch(() => {})),
          );
        } catch {
          // HTTP LAN origins have no service worker.
        }
        location.reload();
      } else if (
        restarting &&
        current.current.tag === restarting &&
        current.phase !== 'restarting'
      ) {
        // A postponed activation or rollback stays on the installed build.
        rememberRestart('');
      }
    } catch (error) {
      if (version === generation && !restarting)
        updateError.value = (error as Error).message;
    }
  })().finally(() => {
    request = undefined;
    schedule();
  });
  return request;
}

export function observeUpdates() {
  listeners++;
  void refreshUpdates();
  return () => {
    listeners--;
    if (!listeners && !restarting) clearTimeout(timer);
  };
}

export async function updateAction(
  action: string,
  channel: string,
  automatic: boolean,
) {
  if (updateBusy.value || indexStatus.value.driving !== 'parked') return;
  generation++;
  updateSubmitting.value = true;
  updateError.value = '';
  const previous = updateState.value;
  // Set before sending: installation may be accepted even if the restart
  // closes the connection before its response reaches this browser.
  if (action === 'install') {
    rememberRestart(previous?.current.tag || '');
    if (previous) updateState.value = { ...previous, phase: 'restarting' };
  }
  try {
    const response = await fetch('/api/updates', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action, channel, automatic }),
      signal: AbortSignal.timeout(10000),
    });
    const result = await response.json();
    if (!response.ok) {
      if (action === 'install') updateState.value = previous;
      rememberRestart('');
      throw new Error(
        result.error
          ? backendMessage(result.error)
          : t('updates.couldNotUpdateDash'),
      );
    }
    updateState.value = result;
  } catch (error) {
    if (!restarting) updateError.value = (error as Error).message;
  } finally {
    updateSubmitting.value = false;
    schedule();
  }
}

if (restarting) void refreshUpdates();
