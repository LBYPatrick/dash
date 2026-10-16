<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useTranslation } from 'i18next-vue';
import { formatNumber } from '../i18n';
import { backendMessage } from '../i18n/backend';
import { formatBytes } from '../library';
import {
  Download,
  FolderOpen,
  LoaderCircle,
  Pause,
  Play,
  Unplug,
  Check,
  ArrowUpRight,
} from '../icons';
import Sheet from './Sheet.vue';
import SelectMenu from './SelectMenu.vue';
import { useToasts } from '../vendor/shelf/toasts';
import {
  transferRequest,
  terminalTransfer,
  type TransferSnapshot,
  type TransferBatch,
  type TransferAuthorization,
  type USBVolume,
} from '../transfers';
const { t } = useTranslation();
const toasts = useToasts();
const setupCommand = '/data/dash/current/dash-backend --configure-transfer -';
const endpointPlaceholder = 'https://…';
const panel = ref<InstanceType<typeof Sheet>>(),
  destinationsPanel = ref<InstanceType<typeof Sheet>>(),
  editor = ref<InstanceType<typeof Sheet>>(),
  authorizationPanel = ref<InstanceType<typeof Sheet>>(),
  issuesPanel = ref<InstanceType<typeof Sheet>>();
const folderPanel = ref<InstanceType<typeof Sheet>>(),
  saveFolder = ref(''),
  folderDraft = ref(''),
  folderError = ref('');
const snapshot = ref<TransferSnapshot>({
  destinations: [],
  batches: [],
  authorizations: [],
  driving: 'unknown',
  configurationSecure: false,
  usbSupported: false,
});
const destination = ref(''),
  autoResume = ref(true),
  error = ref(''),
  editorError = ref(''),
  busy = ref(false),
  opened = ref(false),
  volumes = ref<USBVolume[]>([]),
  refreshingVolumes = ref(false);
const setupKey = ref(''),
  name = ref(''),
  kind = ref<'usb' | 's3' | 'gdrive' | 'onedrive'>('s3'),
  endpoint = ref(''),
  bucket = ref(''),
  region = ref('us-east-1'),
  prefix = ref(''),
  pathStyle = ref(false),
  volumeUUID = ref(''),
  clientID = ref(''),
  clientSecret = ref(''),
  folderID = ref(''),
  tenant = ref('common'),
  accessKey = ref(''),
  secretKey = ref(''),
  sessionToken = ref('');
const authorizationID = ref(''),
  issues = ref<{ path: string; status: string }[]>([]);
let timer: ReturnType<typeof setTimeout> | undefined;
let controller: AbortController | undefined;
let volumeTimer: ReturnType<typeof setTimeout> | undefined;
let volumeController: AbortController | undefined;
const choices = computed(() => [
  ...snapshot.value.destinations.map((value) => ({
    value: value.id,
    label: value.name,
  })),
  ...volumes.value
    .filter(
      (volume) =>
        volume.available &&
        !snapshot.value.destinations.some(
          (item) =>
            item.kind === 'usb' &&
            item.location.toLowerCase() === volume.uuid.toLowerCase(),
        ),
    )
    .map((volume) => ({
      value: `volume:${volume.uuid}`,
      label: `${volume.label || volume.name} · ${volume.filesystem}`,
    })),
]);
const selectedDestination = computed(() =>
  snapshot.value.destinations.find((value) => value.id === destination.value),
);
watch([destination, () => selectedDestination.value?.directory], () => {
  saveFolder.value = selectedDestination.value?.directory || '';
});
function randomDestinationID(provider: string) {
  return `${provider}-${Array.from(crypto.getRandomValues(new Uint8Array(6)), (value) => value.toString(16).padStart(2, '0')).join('')}`;
}
function folderPreview(folder: string) {
  return [folder, 'dash-archive/v1/…'].filter(Boolean).join('/');
}
function editFolder() {
  folderDraft.value = saveFolder.value;
  folderError.value = '';
  folderPanel.value?.showModal();
}
async function applyFolder() {
  if (busy.value) return;
  busy.value = true;
  folderError.value = '';
  try {
    if (selectedDestination.value) {
      await transferRequest(
        `/api/transfer-destinations/${destination.value}/directory`,
        { path: folderDraft.value },
      );
      selectedDestination.value.directory = folderDraft.value;
    }
    saveFolder.value = folderDraft.value;
    folderPanel.value?.close();
  } catch (cause) {
    folderError.value = backendMessage((cause as Error).message);
  } finally {
    busy.value = false;
  }
}
function eta(seconds: number) {
  if (seconds < 60)
    return t('transfers.etaSeconds', { count: Math.ceil(seconds) });
  if (seconds < 3600)
    return t('transfers.etaMinutes', { count: Math.ceil(seconds / 60) });
  return t('transfers.etaHours', {
    count: formatNumber(seconds / 3600, { maximumFractionDigits: 1 }),
  });
}
const active = computed(() =>
  snapshot.value.batches.find((batch) => !terminalTransfer(batch.status)),
);
const recent = computed(() =>
  snapshot.value.batches
    .filter((batch) => terminalTransfer(batch.status))
    .slice(0, 3),
);
const authorization = computed(() =>
  snapshot.value.authorizations.find(
    (value) => value.destinationId === authorizationID.value,
  ),
);
const providers = computed(
  () =>
    [
      { value: 'usb', label: t('transfers.usbDrive') },
      { value: 's3', label: t('transfers.s3Storage') },
      { value: 'gdrive', label: t('transfers.googleDrive') },
      { value: 'onedrive', label: t('transfers.oneDrive') },
    ] as const,
);
const bundledSelected = computed(
  () => !!snapshot.value.bundledProviders?.[kind.value],
);
const canSave = computed(() =>
  kind.value === 'usb'
    ? !!volumeUUID.value
    : bundledSelected.value ||
      (snapshot.value.configurationSecure && !!setupKey.value.trim()),
);
watch(kind, () => {
  clearSecrets();
  if (kind.value === 'usb') void refreshVolumes();
});
function providerLabel(value: string) {
  return (
    providers.value.find((provider) => provider.value === value)?.label || value
  );
}
function destinationName(id: string) {
  return (
    snapshot.value.destinations.find((value) => value.id === id)?.name || id
  );
}
function statusLabel(status: string) {
  return t(
    `transfers.status${status
      .split('_')
      .map((part) => part[0]?.toUpperCase() + part.slice(1))
      .join('')}`,
  );
}
function progress(batch: TransferBatch) {
  return batch.totalBytes
    ? Math.min(1, batch.transferredBytes / batch.totalBytes)
    : batch.totalFiles
      ? batch.completedFiles / batch.totalFiles
      : 0;
}
async function load() {
  if (!opened.value) return;
  controller?.abort();
  const current = new AbortController();
  controller = current;
  try {
    const value = await transferRequest<TransferSnapshot>(
      '/api/transfers',
      undefined,
      undefined,
      current.signal,
    );
    if (current.signal.aborted || controller !== current) return;
    snapshot.value = value;
    if (!destination.value && choices.value.length)
      destination.value = choices.value[0]!.value;
    error.value = '';
  } catch (cause) {
    if (!current.signal.aborted && controller === current)
      error.value = backendMessage((cause as Error).message);
  } finally {
    clearTimeout(timer);
    if (opened.value && controller === current)
      timer = setTimeout(() => void load(), 1500);
  }
}
async function open(mode: 'transfer' | 'destinations' = 'transfer') {
  opened.value = true;
  error.value = '';
  panel.value?.showModal();
  await load();
  void refreshVolumes();
  if (mode === 'destinations') destinationsPanel.value?.showModal();
}
function close() {
  opened.value = false;
  clearTimeout(timer);
  controller?.abort();
  clearTimeout(volumeTimer);
  volumeController?.abort();
  clearSecrets();
}
function clearSecrets() {
  setupKey.value = '';
  accessKey.value = '';
  secretKey.value = '';
  sessionToken.value = '';
  clientSecret.value = '';
}
async function perform(operation: () => Promise<unknown>) {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    await operation();
    await load();
  } catch (cause) {
    error.value = backendMessage((cause as Error).message);
  } finally {
    busy.value = false;
  }
}
async function start() {
  await perform(async () => {
    if (destination.value.startsWith('volume:')) {
      const uuid = destination.value.slice(7),
        volume = volumes.value.find((value) => value.uuid === uuid);
      if (!volume?.available) throw new Error(t('transfers.noVolumes'));
      const id = randomDestinationID('usb');
      await transferRequest('/api/transfer-destinations', {
        id,
        name: volume.label || volume.name,
        kind: 'usb',
        volumeUuid: uuid,
        prefix: saveFolder.value,
        secret: {},
      });
      destination.value = id;
    }
    await transferRequest('/api/transfers', {
      destinationId: destination.value,
      autoResume: autoResume.value,
    });
  });
}
async function action(batch: TransferBatch, value: string) {
  await perform(() =>
    transferRequest(`/api/transfers/${batch.id}/${value}`, {}),
  );
}
async function refreshVolumes() {
  if (
    !opened.value ||
    !snapshot.value.usbSupported ||
    snapshot.value.driving !== 'parked'
  ) {
    if (opened.value)
      volumeTimer = setTimeout(() => void refreshVolumes(), 3000);
    return;
  }
  clearTimeout(volumeTimer);
  volumeController?.abort();
  const current = new AbortController();
  volumeController = current;
  refreshingVolumes.value = true;
  try {
    const result = await transferRequest<USBVolume[]>(
      '/api/transfers/volumes',
      undefined,
      undefined,
      current.signal,
    );
    if (current.signal.aborted || current !== volumeController) return;
    volumes.value = result;
    if (!volumeUUID.value)
      volumeUUID.value =
        volumes.value.find((volume) => volume.available)?.uuid || '';
    if (!destination.value && choices.value.length)
      destination.value = choices.value[0]!.value;
    if (
      destination.value.startsWith('volume:') &&
      !choices.value.some((choice) => choice.value === destination.value)
    )
      destination.value = '';
  } catch (cause) {
    if (!current.signal.aborted)
      editorError.value = backendMessage((cause as Error).message);
  } finally {
    if (current === volumeController) {
      refreshingVolumes.value = false;
      if (opened.value)
        volumeTimer = setTimeout(() => void refreshVolumes(), 3000);
    }
  }
}
function addDestination() {
  clearSecrets();
  name.value = '';
  endpoint.value = '';
  bucket.value = '';
  prefix.value = '';
  clientID.value = '';
  folderID.value = '';
  tenant.value = 'common';
  volumeUUID.value = '';
  pathStyle.value = false;
  editorError.value = '';
  kind.value = snapshot.value.usbSupported ? 'usb' : 's3';
  editor.value?.showModal();
  if (kind.value === 'usb') void refreshVolumes();
}
async function save() {
  if (busy.value || !canSave.value) return;
  busy.value = true;
  editorError.value = '';
  const id = randomDestinationID(kind.value);
  try {
    await transferRequest(
      '/api/transfer-destinations',
      {
        id,
        name: name.value.trim(),
        kind: kind.value,
        endpoint: endpoint.value.trim(),
        bucket: bucket.value.trim(),
        region: region.value.trim(),
        prefix: prefix.value.trim(),
        pathStyle: pathStyle.value,
        volumeUuid: volumeUUID.value,
        clientId: bundledSelected.value ? '' : clientID.value.trim(),
        tenant: tenant.value.trim(),
        folderId: folderID.value.trim(),
        secret:
          kind.value === 's3'
            ? {
                accessKey: accessKey.value,
                secretKey: secretKey.value,
                sessionToken: sessionToken.value,
              }
            : kind.value === 'gdrive' && !bundledSelected.value
              ? { clientSecret: clientSecret.value }
              : {},
      },
      setupKey.value.trim(),
    );
    destination.value = id;
    clearSecrets();
    editor.value?.close();
    await load();
    if (bundledSelected.value) {
      busy.value = false;
      await connect(id);
    }
  } catch (cause) {
    editorError.value = backendMessage((cause as Error).message);
  } finally {
    busy.value = false;
  }
}
async function connect(id: string) {
  authorizationID.value = id;
  await perform(async () => {
    const value = await transferRequest<TransferAuthorization>(
      `/api/transfer-destinations/${id}/authorize`,
      {},
    );
    snapshot.value.authorizations = snapshot.value.authorizations
      .filter((item) => item.destinationId !== id)
      .concat(value);
    authorizationPanel.value?.showModal();
  });
}
async function remove(id: string, operation = 'remove') {
  await perform(() =>
    transferRequest(
      `/api/transfer-destinations/${id}/${operation}`,
      {},
      setupKey.value.trim(),
    ),
  );
  if (operation === 'remove' && destination.value === id)
    destination.value = '';
}
async function eject(uuid: string) {
  await perform(async () => {
    await transferRequest('/api/transfers/eject', { uuid });
    toasts.show({ tone: 'success', message: t('transfers.safeToUnplug') });
    await refreshVolumes();
  });
}
async function showIssues(batch: TransferBatch) {
  await perform(async () => {
    issues.value = await transferRequest(`/api/transfers/${batch.id}/issues`);
    issuesPanel.value?.showModal();
  });
}
onBeforeUnmount(close);
defineExpose({ open });
</script>
<template>
  <Sheet
    ref="panel"
    :title="t('transfers.exportRecordings')"
    :close-label="t('transfers.close')"
    @close="close"
  >
    <div class="modal-inner transfer-panel">
      <p>{{ t('transfers.originalsDescription') }}</p>
      <div v-if="error" class="inline-error" role="alert">{{ error }}</div>
      <section
        v-if="active"
        class="transfer-card"
        :aria-label="t('transfers.currentTransfer')"
      >
        <div class="transfer-heading">
          <strong>{{ destinationName(active.destinationId) }}</strong
          ><LoaderCircle
            v-if="
              [
                'queued',
                'scanning',
                'running',
                'finalizing',
                'canceling',
              ].includes(active.status)
            "
            :size="17"
            class="spinning"
          />
        </div>
        <p role="status">{{ statusLabel(active.status) }}</p>
        <p v-if="active.directory" class="transfer-path">
          {{ folderPreview(active.directory) }}
        </p>
        <div
          class="transfer-progress"
          role="progressbar"
          :aria-label="t('transfers.progress')"
          :aria-valuenow="Math.round(progress(active) * 100)"
          :aria-valuemin="0"
          :aria-valuemax="100"
        >
          <span :style="{ transform: `scaleX(${progress(active)})` }" />
        </div>
        <p class="transfer-detail">
          {{
            t('transfers.fileProgress', {
              complete: formatNumber(active.completedFiles),
              total: formatNumber(active.totalFiles),
            })
          }}
          · {{ formatBytes(active.transferredBytes) }} /
          {{ formatBytes(active.totalBytes) }}
        </p>
        <dl v-if="active.status === 'running'" class="transfer-metrics">
          <div>
            <dt>{{ t('transfers.transferRate') }}</dt>
            <dd>
              {{
                active.bytesPerSecond
                  ? t('transfers.rateValue', {
                      rate: formatBytes(active.bytesPerSecond),
                    })
                  : t('transfers.measuring')
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('transfers.timeRemaining') }}</dt>
            <dd>
              {{
                active.etaSeconds != null
                  ? eta(active.etaSeconds)
                  : t('transfers.estimating')
              }}
            </dd>
          </div>
        </dl>
        <p
          v-if="active.progressReady === false"
          class="transfer-detail"
          role="status"
        >
          {{ t('transfers.restoringProgress') }}
        </p>
        <p v-if="active.reusedBytes" class="transfer-detail">
          {{
            t('transfers.reusedBytes', {
              size: formatBytes(active.reusedBytes),
            })
          }}
        </p>
        <p
          v-if="active.currentFile"
          class="transfer-path"
          :title="active.currentFile"
        >
          {{ active.currentFile }}
        </p>
        <p
          v-if="active.status === 'needs_attention'"
          class="inline-error"
          role="alert"
        >
          {{ backendMessage(active.message) }}
        </p>
        <div class="transfer-actions">
          <button
            v-if="
              ['paused', 'needs_attention', 'waiting_parked'].includes(
                active.status,
              )
            "
            class="button"
            :disabled="busy || snapshot.driving !== 'parked'"
            @click="action(active, 'resume')"
          >
            <Play :size="15" />{{ t('transfers.resume') }}
          </button>
          <button
            v-else-if="active.status !== 'canceling'"
            class="button"
            :disabled="busy"
            @click="action(active, 'pause')"
          >
            <Pause :size="15" />{{ t('transfers.pause') }}
          </button>
          <button
            class="text-button"
            :disabled="busy || active.status === 'canceling'"
            @click="action(active, 'cancel')"
          >
            {{ t('transfers.cancel') }}
          </button>
          <button
            v-if="active.issues"
            class="text-button"
            :disabled="busy"
            @click="showIssues(active)"
          >
            {{ t('transfers.reviewIssues', { count: active.issues }) }}
          </button>
        </div>
      </section>
      <form v-else @submit.prevent="start">
        <label for="batch-destination" class="field-label">{{
          t('transfers.destination')
        }}</label>
        <SelectMenu
          id="batch-destination"
          v-model="destination"
          :aria-label="t('transfers.destination')"
          :disabled="!choices.length || busy"
          :options="choices"
        />
        <p v-if="!choices.length" class="transfer-detail">
          {{ t('transfers.noDestinations') }}
        </p>
        <p
          v-if="snapshot.usbSupported && !volumes.length"
          class="transfer-detail"
          role="status"
        >
          {{ t('transfers.noVolumes') }}
        </p>
        <TransitionGroup name="usb-arrival" tag="div" class="usb-arrivals">
          <span
            v-for="volume in volumes.filter((value) => value.available)"
            :key="volume.uuid"
            class="transfer-detail"
            ><Unplug :size="15" />{{
              t('transfers.driveDetected', {
                name: volume.label || volume.name,
              })
            }}</span
          >
        </TransitionGroup>
        <button
          v-if="destination"
          class="folder-choice"
          type="button"
          :disabled="busy"
          @click="editFolder"
        >
          <FolderOpen :size="18" /><span
            ><strong>{{ t('transfers.saveFolder') }}</strong
            ><small>{{
              saveFolder || t('transfers.destinationRoot')
            }}</small></span
          ><span aria-hidden="true">›</span>
        </button>
        <p v-if="destination" class="transfer-path">
          {{ folderPreview(saveFolder) }}
        </p>
        <label class="checkbox-field"
          ><input v-model="autoResume" type="checkbox" />{{
            t('transfers.resumeWhenParked')
          }}</label
        >
        <p
          v-if="snapshot.driving !== 'parked'"
          class="transfer-detail"
          role="status"
        >
          {{ t('transfers.parkedOnly') }}
        </p>
        <button
          type="submit"
          class="button primary full-width"
          :disabled="
            busy ||
            !destination ||
            snapshot.driving !== 'parked' ||
            (!!selectedDestination && !selectedDestination.connected)
          "
        >
          <LoaderCircle v-if="busy" class="spinning" :size="17" /><Download
            v-else
            :size="17"
          />{{ t('transfers.copyAllOriginals') }}
        </button>
      </form>
      <button
        class="button full-width"
        type="button"
        @click="destinationsPanel?.showModal()"
      >
        <FolderOpen :size="17" />{{ t('transfers.manageDestinations') }}
      </button>
      <p class="transfer-detail">{{ t('transfers.backgroundDescription') }}</p>
      <section
        v-if="recent.length"
        class="transfer-history"
        :aria-label="t('transfers.recentTransfers')"
      >
        <h3>{{ t('transfers.recentTransfers') }}</h3>
        <div
          v-for="batch in recent"
          :key="batch.id"
          class="transfer-history-row"
        >
          <span
            ><strong>{{ destinationName(batch.destinationId) }}</strong
            ><small
              >{{ statusLabel(batch.status) }} ·
              {{ formatBytes(batch.completedBytes) }}</small
            ><small v-if="batch.cleanupPending">{{
              t('transfers.cleanupPending')
            }}</small></span
          ><button
            v-if="batch.issues"
            class="text-button"
            @click="showIssues(batch)"
          >
            {{ t('transfers.reviewIssues', { count: batch.issues }) }}</button
          ><Check v-else-if="batch.status === 'complete'" :size="17" />
        </div>
      </section>
    </div>
  </Sheet>
  <Sheet
    ref="destinationsPanel"
    :title="t('transfers.destinations')"
    :close-label="t('transfers.close')"
    over-sheets
  >
    <div class="modal-inner transfer-panel">
      <p v-if="!snapshot.destinations.length">
        {{ t('transfers.noDestinations') }}
      </p>
      <div v-if="error" class="inline-error" role="alert">{{ error }}</div>
      <div
        v-for="value in snapshot.destinations"
        :key="value.id"
        class="transfer-card"
      >
        <div class="transfer-heading">
          <strong>{{ value.name }}</strong
          ><small>{{ providerLabel(value.kind) }}</small>
        </div>
        <p v-if="value.location" class="transfer-path">{{ value.location }}</p>
        <p
          v-if="value.kind === 'gdrive' || value.kind === 'onedrive'"
          class="transfer-detail"
        >
          {{
            value.connected
              ? t('transfers.connected')
              : t('transfers.authorizationRequired')
          }}
        </p>
        <div class="transfer-actions">
          <button
            v-if="value.kind === 'gdrive' || value.kind === 'onedrive'"
            class="button"
            :disabled="busy || snapshot.driving !== 'parked'"
            @click="connect(value.id)"
          >
            {{
              value.connected
                ? t('transfers.reconnect')
                : t('transfers.connect')
            }}</button
          ><button
            v-if="value.kind === 'usb'"
            class="button"
            :disabled="busy"
            @click="eject(value.location)"
          >
            <Unplug :size="15" />{{ t('transfers.eject') }}</button
          ><button
            v-if="
              value.kind === 'usb' || (snapshot.configurationSecure && setupKey)
            "
            class="text-button"
            :disabled="busy"
            @click="remove(value.id)"
          >
            {{ t('transfers.removeDestination') }}
          </button>
          <button
            v-if="
              value.connected &&
              ['gdrive', 'onedrive'].includes(value.kind) &&
              snapshot.configurationSecure &&
              setupKey
            "
            class="text-button"
            :disabled="busy"
            @click="remove(value.id, 'disconnect')"
          >
            {{ t('transfers.disconnect') }}
          </button>
        </div>
      </div>
      <button
        type="button"
        class="button primary full-width"
        @click="addDestination"
      >
        {{ t('transfers.addDestination') }}
      </button>
      <details
        v-if="
          snapshot.configurationSecure &&
          snapshot.destinations.some((value) => value.kind !== 'usb')
        "
      >
        <summary>{{ t('transfers.manageAuthorization') }}</summary>
        <label class="field-label" for="transfer-management-key">{{
          t('transfers.setupKey')
        }}</label>
        <input
          id="transfer-management-key"
          v-model="setupKey"
          type="password"
          autocomplete="off"
          class="transfer-input"
        />
        <p class="transfer-detail">{{ t('transfers.setupKeyHelp') }}</p>
      </details>
    </div>
  </Sheet>
  <Sheet
    ref="editor"
    :title="t('transfers.addDestination')"
    :close-label="t('transfers.close')"
    over-sheets
    @close="clearSecrets"
  >
    <form class="modal-inner transfer-panel" @submit.prevent="save">
      <label class="field-label" for="transfer-provider">{{
        t('transfers.provider')
      }}</label
      ><SelectMenu
        id="transfer-provider"
        v-model="kind"
        :aria-label="t('transfers.provider')"
        :options="providers"
      />
      <label class="field-label" for="transfer-name">{{
        t('transfers.name')
      }}</label
      ><input
        id="transfer-name"
        v-model="name"
        class="transfer-input"
        maxlength="80"
        required
        autocomplete="off"
      />
      <template v-if="kind === 'usb'">
        <p class="transfer-detail">{{ t('transfers.usbExperimental') }}</p>
        <label class="field-label" for="transfer-volume">{{
          t('transfers.usbVolume')
        }}</label
        ><SelectMenu
          id="transfer-volume"
          v-model="volumeUUID"
          :aria-label="t('transfers.usbVolume')"
          :disabled="!volumes.some((volume) => volume.available)"
          :options="
            volumes
              .filter((volume) => volume.available)
              .map((volume) => ({
                value: volume.uuid,
                label: `${volume.label || volume.name} · ${volume.filesystem}`,
              }))
          "
        />
        <p v-if="!refreshingVolumes && !volumes.length" class="transfer-detail">
          {{ t('transfers.noVolumes') }}
        </p>
      </template>
      <template v-else-if="bundledSelected"
        ><p class="transfer-detail">
          {{ t('transfers.bundledAuthorization') }}
        </p></template
      >
      <template v-else-if="!snapshot.configurationSecure"
        ><p class="inline-error">{{ t('transfers.secureSetup') }}</p>
        <p class="transfer-detail">{{ t('transfers.sshSetup') }}</p>
        <pre class="transfer-command">{{ setupCommand }}</pre>
      </template>
      <template v-else>
        <label class="field-label" for="transfer-setup-key">{{
          t('transfers.setupKey')
        }}</label
        ><input
          id="transfer-setup-key"
          v-model="setupKey"
          type="password"
          autocomplete="off"
          class="transfer-input"
          required
        />
        <p class="transfer-detail">{{ t('transfers.setupKeyHelp') }}</p>
        <template v-if="kind === 's3'">
          <label class="field-label" for="transfer-endpoint">{{
            t('transfers.endpoint')
          }}</label
          ><input
            id="transfer-endpoint"
            v-model="endpoint"
            type="url"
            :placeholder="endpointPlaceholder"
            class="transfer-input"
            autocomplete="off"
          />
          <label class="field-label" for="transfer-bucket">{{
            t('transfers.bucket')
          }}</label
          ><input
            id="transfer-bucket"
            v-model="bucket"
            class="transfer-input"
            required
            autocomplete="off"
          />
          <label class="field-label" for="transfer-region">{{
            t('transfers.region')
          }}</label
          ><input
            id="transfer-region"
            v-model="region"
            class="transfer-input"
            required
            autocomplete="off"
          />
          <label class="field-label" for="transfer-access-key">{{
            t('transfers.accessKey')
          }}</label
          ><input
            id="transfer-access-key"
            v-model="accessKey"
            type="password"
            class="transfer-input"
            required
            autocomplete="off"
          />
          <label class="field-label" for="transfer-secret-key">{{
            t('transfers.secretKey')
          }}</label
          ><input
            id="transfer-secret-key"
            v-model="secretKey"
            type="password"
            class="transfer-input"
            required
            autocomplete="off"
          />
          <label class="field-label" for="transfer-session-token">{{
            t('transfers.sessionToken')
          }}</label
          ><input
            id="transfer-session-token"
            v-model="sessionToken"
            type="password"
            class="transfer-input"
            autocomplete="off"
          />
          <label class="checkbox-field"
            ><input v-model="pathStyle" type="checkbox" />{{
              t('transfers.pathStyle')
            }}</label
          >
        </template>
        <template v-else>
          <p class="transfer-detail">
            {{ t('transfers.registeredApplication') }}
          </p>
          <label class="field-label" for="transfer-client-id">{{
            t('transfers.clientId')
          }}</label
          ><input
            id="transfer-client-id"
            v-model="clientID"
            class="transfer-input"
            required
            autocomplete="off"
          />
          <label
            v-if="kind === 'gdrive'"
            class="field-label"
            for="transfer-client-secret"
            >{{ t('transfers.clientSecret') }}</label
          ><input
            v-if="kind === 'gdrive'"
            id="transfer-client-secret"
            v-model="clientSecret"
            class="transfer-input"
            type="password"
            autocomplete="off"
          />
          <label
            v-if="kind === 'onedrive'"
            class="field-label"
            for="transfer-tenant"
            >{{ t('transfers.tenant') }}</label
          ><input
            v-if="kind === 'onedrive'"
            id="transfer-tenant"
            v-model="tenant"
            class="transfer-input"
            autocomplete="off"
          />
          <label class="field-label" for="transfer-folder-id">{{
            t('transfers.folderId')
          }}</label
          ><input
            id="transfer-folder-id"
            v-model="folderID"
            class="transfer-input"
            autocomplete="off"
          />
        </template>
      </template>
      <label class="field-label" for="transfer-prefix">{{
        t('transfers.saveFolder')
      }}</label>
      <input
        id="transfer-prefix"
        v-model="prefix"
        class="transfer-input"
        autocomplete="off"
        autocapitalize="off"
        :spellcheck="false"
        maxlength="512"
        :placeholder="t('transfers.destinationRoot')"
      />
      <p class="transfer-detail">{{ t('transfers.folderHelp') }}</p>
      <p class="transfer-path">{{ folderPreview(prefix) }}</p>
      <p v-if="editorError" class="inline-error" role="alert">
        {{ editorError }}
      </p>
      <button
        class="button primary full-width"
        type="submit"
        :disabled="busy || !canSave || !name.trim()"
      >
        <LoaderCircle v-if="busy" class="spinning" :size="17" />{{
          t('transfers.saveDestination')
        }}
      </button>
    </form>
  </Sheet>
  <Sheet
    ref="folderPanel"
    :title="t('transfers.saveFolder')"
    :close-label="t('transfers.close')"
    over-sheets
  >
    <form class="modal-inner transfer-panel" @submit.prevent="applyFolder">
      <label for="batch-folder" class="field-label">{{
        t('transfers.folderPath')
      }}</label>
      <input
        id="batch-folder"
        v-model="folderDraft"
        class="transfer-input"
        maxlength="512"
        autocomplete="off"
        autocapitalize="off"
        :spellcheck="false"
        :placeholder="t('transfers.destinationRoot')"
      />
      <p class="transfer-detail">{{ t('transfers.folderHelp') }}</p>
      <p class="transfer-path">{{ folderPreview(folderDraft) }}</p>
      <p v-if="folderError" class="inline-error" role="alert">
        {{ folderError }}
      </p>
      <button class="button primary full-width" type="submit" :disabled="busy">
        {{ t('transfers.useFolder') }}
      </button>
    </form>
  </Sheet>
  <Sheet
    ref="authorizationPanel"
    :title="t('transfers.connectProvider')"
    :close-label="t('transfers.close')"
    over-sheets
  >
    <div class="modal-inner transfer-panel">
      <p role="status">
        {{
          authorization
            ? statusLabel(authorization.status)
            : t('transfers.authorizationRequired')
        }}
      </p>
      <template v-if="authorization?.userCode"
        ><p>{{ t('transfers.authorizationInstructions') }}</p>
        <code class="transfer-code">{{ authorization.userCode }}</code
        ><a
          class="button primary full-width"
          :href="authorization.verificationUri"
          target="_blank"
          rel="noopener noreferrer"
          >{{ t('transfers.openProvider')
          }}<ArrowUpRight :size="17" /></a></template
      ><button
        v-if="
          authorization && ['failed', 'expired'].includes(authorization.status)
        "
        class="button full-width"
        :disabled="busy || snapshot.driving !== 'parked'"
        @click="connect(authorizationID)"
      >
        {{ t('transfers.reconnect') }}
      </button>
    </div>
  </Sheet>
  <Sheet
    ref="issuesPanel"
    :title="t('transfers.issues')"
    :close-label="t('transfers.close')"
    over-sheets
    ><div class="modal-inner transfer-panel">
      <p>{{ t('transfers.issuesDescription') }}</p>
      <div
        v-for="issue in issues"
        :key="issue.path"
        class="transfer-history-row"
      >
        <span class="transfer-path">{{ issue.path }}</span
        ><small>{{ statusLabel(issue.status) }}</small>
      </div>
    </div></Sheet
  >
</template>
<style scoped>
.transfer-panel {
  --muted: color-mix(in oklab, var(--ink) 78%, var(--surface-raised));
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}
.transfer-panel .button.primary {
  background: var(--color-primary);
  color: var(--color-primary-content);
}
.transfer-panel .button,
.transfer-panel .text-button {
  min-height: 44px;
}
.transfer-panel .transfer-path {
  color: var(--ink);
}
.transfer-panel .inline-error {
  color: var(--danger-text);
  background: var(--danger-fill);
  border: 0;
  font-size: 13px;
}
.transfer-input:focus-visible {
  outline: 0;
  box-shadow: inset 0 -3px 0 var(--color-primary-text);
}
.transfer-panel summary {
  min-height: 44px;
  display: flex;
  align-items: center;
  cursor: pointer;
}
.transfer-panel p,
.transfer-panel h3 {
  margin: 0;
}
.transfer-panel form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.transfer-card {
  padding: 16px;
  background: var(--surface-raised);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}
.transfer-heading,
.transfer-history-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.transfer-heading strong,
.transfer-history-row strong {
  overflow-wrap: anywhere;
}
.transfer-detail,
.transfer-heading small,
.transfer-history-row small {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.5;
}
.transfer-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.transfer-path {
  font-size: 12px;
  overflow-wrap: anywhere;
  min-width: 0;
}
.transfer-progress {
  height: 5px;
  background: var(--surface-subtle);
  overflow: hidden;
}
.transfer-progress span {
  display: block;
  width: 100%;
  height: 100%;
  background: var(--color-primary-text);
  transform-origin: left;
  transition: transform 180ms linear;
}
.transfer-input {
  width: 100%;
  min-width: 0;
  min-height: 44px;
  padding: 10px 12px;
  border: 0;
  border-radius: 6px;
  background: var(--surface-raised);
  color: var(--ink);
  font: inherit;
  font-size: 16px;
}
.transfer-command {
  padding: 12px;
  background: var(--surface-raised);
  font-size: 12px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.transfer-code {
  padding: 16px;
  text-align: center;
  font-size: 28px;
  letter-spacing: 0.1em;
  background: var(--surface-raised);
  user-select: all;
}
.transfer-history {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.transfer-history-row {
  min-height: 44px;
}
.transfer-history-row span {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.folder-choice {
  border: 0;
  border-radius: 6px;
  background: var(--surface-raised);
  color: var(--ink);
  padding: 12px;
  display: flex;
  align-items: center;
  gap: 12px;
  text-align: start;
  cursor: pointer;
  min-height: 56px;
}
.folder-choice > span:first-of-type {
  flex: 1;
  min-width: 0;
  display: grid;
  gap: 4px;
  overflow-wrap: anywhere;
}
.folder-choice small {
  color: var(--muted);
}
.transfer-metrics {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
  margin: 0;
}
.transfer-metrics dt {
  color: var(--muted);
  font-size: 12px;
}
.transfer-metrics dd {
  margin: 4px 0 0;
  font-size: 15px;
  overflow-wrap: anywhere;
}
.transfer-metrics,
.transfer-detail {
  font-variant-numeric: tabular-nums;
}
.usb-arrivals:empty {
  display: none;
}
.usb-arrivals > span {
  display: flex;
  gap: 8px;
  align-items: center;
}
.usb-arrival-enter-active,
.usb-arrival-leave-active {
  transition:
    opacity 180ms ease-out,
    transform 180ms cubic-bezier(0.23, 1, 0.32, 1);
}
.usb-arrival-enter-from,
.usb-arrival-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
@media (prefers-reduced-motion: reduce) {
  .transfer-progress span {
    transition: none;
  }
  .usb-arrival-enter-active,
  .usb-arrival-leave-active {
    transition: none;
  }
}
</style>
