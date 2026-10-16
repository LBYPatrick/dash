export interface TransferDestination {
  id: string;
  name: string;
  kind: 'usb' | 's3' | 'gdrive' | 'onedrive';
  location: string;
  connected: boolean;
  directory?: string;
}
export interface TransferBatch {
  id: string;
  destinationId: string;
  status: string;
  message: string;
  created: number;
  totalFiles: number;
  totalBytes: number;
  completedFiles: number;
  completedBytes: number;
  transferredBytes: number;
  issues: number;
  currentFile?: string;
  autoResume: boolean;
  cleanupPending?: boolean;
  directory?: string;
  progressReady?: boolean;
  bytesPerSecond?: number;
  etaSeconds?: number;
  reusedBytes?: number;
}
export interface TransferAuthorization {
  destinationId: string;
  status: string;
  userCode?: string;
  verificationUri?: string;
  expiresAt?: number;
}
export interface TransferSnapshot {
  destinations: TransferDestination[];
  batches: TransferBatch[];
  authorizations: TransferAuthorization[];
  driving: string;
  configurationSecure: boolean;
  usbSupported: boolean;
  bundledProviders?: Record<string, boolean>;
}
export interface USBVolume {
  uuid: string;
  label: string;
  filesystem: string;
  name: string;
  mounted: boolean;
  available: boolean;
}
export const terminalTransfer = (status: string) =>
  ['complete', 'complete_with_issues', 'canceled'].includes(status);
export async function transferRequest<T>(
  path: string,
  body?: unknown,
  key?: string,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(key ? { Authorization: `DashSetup ${key}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal || AbortSignal.timeout(20000),
  });
  const result = await response.json();
  if (!response.ok)
    throw new Error(
      result.error || `Transfer request failed (${response.status})`,
    );
  return result as T;
}
