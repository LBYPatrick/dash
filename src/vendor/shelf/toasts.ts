// Adapted from Shelf's stores/toasts.ts. The web app uses a Vue singleton;
// Shelf's Pinia/Electron notification bridge is not needed in the browser.
import { ref } from 'vue';
export type ToastTone = 'info' | 'success' | 'warning' | 'error';
export interface ToastAction {
  readonly label: string;
  readonly run: () => void;
}
export interface Toast {
  readonly id: string;
  readonly message: string;
  readonly title?: string;
  readonly tone: ToastTone;
  readonly action?: ToastAction;
  readonly expire?: number;
}
const toasts = ref<Toast[]>([]);
let counter = 0;
function show(toast: Omit<Toast, 'id'> & { id?: string }): string {
  const id = toast.id ?? `toast-${++counter}`;
  toasts.value = [
    ...toasts.value.filter((existing) => existing.id !== id),
    { expire: 5000, ...toast, id },
  ];
  return id;
}
function dismiss(id: string) {
  toasts.value = toasts.value.filter((toast) => toast.id !== id);
}
function clear() {
  toasts.value = [];
}
export function useToasts() {
  return { toasts, show, dismiss, clear };
}
