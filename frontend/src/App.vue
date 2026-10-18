<script setup lang="ts">
import ToastStack from './vendor/shelf/ToastStack.vue';
import { useToasts } from './vendor/shelf/toasts';
const notifications = useToasts();
import { useTranslation } from 'i18next-vue';
import {
  computed,
  defineAsyncComponent,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from 'vue';
import {
  ArrowUpRight,
  ArrowRight,
  ArrowLeft,
  Bookmark,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Download,
  FolderOpen,
  HardDrive,
  ListVideo,
  LoaderCircle,
  Monitor,
  Menu,
  MoreHorizontal,
  RefreshCw,
  Search,
  ShieldCheck,
  Unplug,
  Wifi,
  X,
} from './icons';
import { vGlassTabs } from './glassTabs';
import SettingsPopup from './components/SettingsPopup.vue';
import TransfersSheet from './components/TransfersSheet.vue';
import HoverTip from './vendor/shelf/HoverTip.vue';
import { vTip, hideTip } from './vendor/shelf/hoverTip';
import Sheet from './components/Sheet.vue';
import SelectMenu from './components/SelectMenu.vue';
import SessionActions from './components/SessionActions.vue';
import RecordingPlayer from './components/RecordingPlayer.vue';
import SessionFacts from './components/SessionFacts.vue';
import { WorkspaceHistory, type WorkspaceRoute } from './workspaceHistory';
import { retainedLiveCapture } from './liveCapture';
import ResizablePanels from './components/ResizablePanels.vue';
import SidebarResize from './components/SidebarResize.vue';
import DeviceSwitcher from './components/DeviceSwitcher.vue';
const sidebarWidth = ref(320);
const sidebarResetting = ref(false);
import ExportDialog from './components/ExportDialog.vue';
const RecordingDashboard = defineAsyncComponent(
  () => import('./components/RecordingDashboard.vue'),
);
const dashboardSelection = ref('');
const TripMap = defineAsyncComponent(() => import('./components/TripMap.vue'));
import WorkspaceNavigation from './components/WorkspaceNavigation.vue';
import LiveView from './components/LiveView.vue';
import DiagnosticsDownload from './components/DiagnosticsDownload.vue';
import SimulationControls from './components/SimulationControls.vue';
import {
  indexEntries,
  indexStatus,
  backendOrigin,
  startIndexing,
  stopIndexing,
  recordingAddress,
  recordingReadiness,
  distanceKm,
  type Position,
  type Telemetry,
} from './indexer';
import {
  downloadUrl,
  formatBytes,
  groupDrives,
  recordingDate,
  listFolder,
  compareCameras,
  preferredCamera,
  type Drive,
  type Entry,
} from './library';
import { demoDrives, demoFiles } from './demo';
import {
  timelinePositions,
  positionAt,
  type TimelineSegment,
} from './viewerTimeline';
const playbackTime = ref(0);
const playbackDuration = ref(0);
import { locale, formatNumber } from './i18n';
import { readStored, writeStored } from './storage';
const { t } = useTranslation();

const appOrigin = location.origin;
const address = ref(new URL('/files/', location.origin).href);
const connectedAddress = ref('');
watch(connectedAddress, () => (dashboardSelection.value = ''));
const demo = ref(false);
const view = ref<'drives' | 'overview' | 'live'>('drives');
const navigationDrawer = ref<HTMLDialogElement>();
const mobileMenu = ref<HTMLButtonElement>();
function showHelp() {
  hideTip();
  if (navigationDrawer.value?.open) {
    navigationDrawer.value.close();
    mobileMenu.value?.focus();
  }
  helpDialog.value?.showModal();
}
const returnView = ref<'drives' | 'overview'>('drives');
const backLabel = computed(() =>
  returnView.value === 'overview'
    ? t('app.backToOverview')
    : t('app.allDrives'),
);
const scrollPositions = new Map<string, number>();
const routeFocus = new Map<string, HTMLElement>();
const routeKey = (route: WorkspaceRoute) =>
  route.driveId ? `drive:${route.driveId}` : route.view;
function rememberScroll() {
  const pane = document.querySelector<HTMLElement>(
    view.value === 'drives' && !mobileDetail.value
      ? '.drive-scroll'
      : '.detail',
  );
  if (pane)
    scrollPositions.set(
      view.value === 'drives' && mobileDetail.value && activeId.value
        ? `drive:${activeId.value}`
        : view.value,
      pane.scrollTop,
    );
}
async function applyWorkspace(route: WorkspaceRoute) {
  const focused =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  const previousKey =
    view.value === 'drives' && mobileDetail.value && activeId.value
      ? `drive:${activeId.value}`
      : view.value;
  if (
    focused &&
    focused !== document.body &&
    !previousKey.startsWith('drive:') &&
    !focused.closest('.scrim, dialog, .workspace-navigation')
  )
    routeFocus.set(previousKey, focused);
  rememberScroll();
  view.value = route.view;
  returnView.value = route.returnView || 'drives';
  const drive =
    route.driveId && drives.value.find((item) => item.id === route.driveId);
  if (drive && readiness(drive).ready) {
    if (activeId.value !== drive.id) sessionPane.value = 'video';
    activeId.value = drive.id;
    segmentIndex.value = 0;
  }
  mobileDetail.value =
    route.view !== 'drives' || !!(drive && readiness(drive).ready);
  navigationDrawer.value?.close();
  await nextTick();
  const pane = document.querySelector<HTMLElement>(
    route.view === 'drives' && !route.driveId ? '.drive-scroll' : '.detail',
  );
  if (pane) pane.scrollTop = scrollPositions.get(routeKey(route)) || 0;
  // Preserve the desktop sidebar's focus; move it when the source is hidden or
  // replaced so keyboard and screen-reader users arrive with the new content.
  if (
    focused &&
    focused !== document.body &&
    (!focused.isConnected ||
      !focused.checkVisibility() ||
      focused.closest('dialog'))
  ) {
    const remembered = routeFocus.get(routeKey(route));
    const destination =
      remembered?.isConnected && remembered.checkVisibility()
        ? remembered
        : document.querySelector<HTMLElement>(
            route.view === 'drives' && !route.driveId
              ? '.sidebar-heading h2, .sidebar-heading h1'
              : '.detail h1, .detail h2',
          );
    if (destination) {
      if (!destination.matches('button, input, a[href], [tabindex]'))
        destination.tabIndex = -1;
      destination.focus({ preventScroll: true });
    }
  }
}
const workspaceHistory = new WorkspaceHistory((route) => {
  void applyWorkspace(route);
});
function navigate(destination: 'drives' | 'overview' | 'live') {
  rememberScroll();
  workspaceHistory.navigate({ view: destination });
}
function returnFromDrive() {
  rememberScroll();
  workspaceHistory.back({ view: returnView.value });
}
const mobileViewport = window.matchMedia('(max-width: 900px)');
const narrow = ref(mobileViewport.matches);
const mobilePlaybackDock = ref<HTMLDivElement>();
const transportTarget = computed(() =>
  narrow.value ? mobilePlaybackDock.value : undefined,
);
function viewportChanged(event: MediaQueryListEvent) {
  narrow.value = event.matches;
}
mobileViewport.addEventListener('change', viewportChanged);
const tripDetails = ref<InstanceType<typeof Sheet>>();

function switchDevice() {
  if (navigationDrawer.value?.open) {
    navigationDrawer.value.close();
    mobileMenu.value?.focus();
  }
  openConnection();
}
const sort = ref<'newest' | 'oldest' | 'longest'>('newest');
const exportDialog = ref<InstanceType<typeof ExportDialog>>();
const transfersDialog = ref<InstanceType<typeof TransfersSheet>>();
const drivePoints = computed<Position[]>((previous) => {
  const points = (
    active.value?.segments.flatMap(
      (segment) => indexEntries.value[segment.url]?.metadata?.points || [],
    ) || []
  ).sort((a, b) => a.time - b.time);
  // Summary polling preserves detailed GPS objects. Keep the route identity
  // stable so it is not rebuilt every 2.5 seconds during playback.
  return previous &&
    previous.length === points.length &&
    points.every((point, index) => point === previous[index])
    ? previous
    : points;
});
const timelineSegments = computed<TimelineSegment[]>(() => {
  let offset = 0;
  const camera = selectedSource.value
    ? new URL(selectedSource.value, location.origin).searchParams.get(
        'camera',
      ) || 'qcamera.ts'
    : 'qcamera.ts';
  return (active.value?.segments || []).flatMap((segment) => {
    const entry = indexEntries.value[segment.url];
    if (
      !demo.value &&
      entry &&
      !entry.files.some(
        (file) =>
          file.name === camera && file.size > 0 && (file.duration || 0) > 0,
      )
    )
      return [];
    const file = entry?.files.find((file) => file.name === camera);
    const origin = file?.native?.originMonotonic;
    const duration =
      file?.duration || entry?.metadata?.duration || (demo.value ? 8 : 60);
    const part = {
      start: offset,
      duration,
      thumbnail: entry?.thumbnail,
      metadata:
        origin && entry?.metadata
          ? {
              ...entry.metadata,
              startMonotonic: origin,
            }
          : entry?.metadata,
    };
    offset += duration;
    return [part];
  });
});
const timedPositions = computed(() =>
  timelinePositions(timelineSegments.value),
);
const playbackPosition = computed(() =>
  positionAt(timedPositions.value, playbackTime.value),
);

const driveMetadata = computed(
  () =>
    active.value?.segments
      .map((segment) => indexEntries.value[segment.url]?.metadata)
      .filter((meta): meta is Telemetry => meta?.status === 'ready') || [],
);
const thumbnail = (drive: Drive) =>
  demo.value
    ? '/sample-road.svg'
    : drive.segments
        .map((segment) => indexEntries.value[segment.url]?.thumbnail)
        .find(Boolean);
function indexDrives(rebuild = false) {
  if (demo.value) {
    tell(t('app.sampleLibraryIsAlreadyIndexed'));
    return;
  }
  void startIndexing(connectedAddress.value, drives.value, rebuild).catch(
    (error) => tell((error as Error).message),
  );
}

const listedDrives = ref<Drive[]>([]);
const drives = computed(() =>
  listedDrives.value
    .map((drive) => recordingDate(drive, indexEntries.value))
    .sort((a, b) => b.date - a.date || b.id.localeCompare(a.id)),
);
const activeId = ref('');
const segmentIndex = ref(0);
const files = ref<Entry[]>([]);
const selectedSource = ref('');
const busy = ref(false);
const filesBusy = ref(false);
const connectionError = ref('');
const fileError = ref('');

const query = ref('');
const filter = ref<'all' | 'saved'>('all');
const mobileDetail = ref(false);
const connectionDialog = ref<InstanceType<typeof Sheet>>();
const helpDialog = ref<InstanceType<typeof Sheet>>();
const sessionPane = ref<'video' | 'data'>('video');
const actionMenu = ref<InstanceType<typeof SessionActions>>();
const originalFilesDialog = ref<InstanceType<typeof Sheet>>();
const storedBookmarks = readStored<unknown>('dash.bookmarks', []);
const bookmarks = ref<string[]>(
  Array.isArray(storedBookmarks)
    ? storedBookmarks.filter((v): v is string => typeof v === 'string')
    : [],
);
const online = ref(navigator.onLine);
let connectionRequest: AbortController | undefined;
let fileRequest: AbortController | undefined;

const readinessById = computed(
  () =>
    new Map(
      drives.value.map((drive) => [
        drive.id,
        recordingReadiness(drive, indexEntries.value, demo.value),
      ]),
    ),
);
const active = computed(() => {
  const drive = drives.value.find((drive) => drive.id === activeId.value);
  return drive && readiness(drive).ready ? drive : undefined;
});
function readiness(drive: Drive) {
  return (
    readinessById.value.get(drive.id) ||
    recordingReadiness(drive, indexEntries.value, demo.value)
  );
}
function recordingName(drive: Drive) {
  return drive.date
    ? timeLabel(drive.date)
    : t('app.recordingNumber', {
        number: /^[a-f0-9]{8}--/i.test(drive.id)
          ? parseInt(drive.id.slice(0, 8), 16)
          : drive.id.slice(0, 18),
      });
}
function readinessLabel(drive: Drive) {
  const state = readiness(drive);
  if (state.empty) return t('app.noPlayableFootage');
  if (state.ready)
    return t('common.segments', { count: drive.segments.length });
  if (indexStatus.value.driving !== 'parked') return t('app.indexingPaused');
  return t(
    indexStatus.value.current?.startsWith(drive.id + '--')
      ? 'app.preparingRecording'
      : 'app.notIndexedYet',
    { completed: state.complete, total: state.total },
  );
}
async function prepareNext(drive: Drive, camera = 'qcamera.ts') {
  try {
    const response = await fetch(
      `/api/playback/prepare?${new URLSearchParams({ drive: drive.id, camera })}`,
      { method: 'POST' },
    );
    if (!response.ok) throw new Error();
    indexStatus.value.priority = drive.id;
  } catch {
    tell(t('app.couldNotPrioritize'));
  }
}
watch(indexEntries, () => {
  // A restored route can precede the first index snapshot. Wait for readiness
  // without losing that destination or opening unindexed footage.
  const route = workspaceHistory.current;
  const restored =
    route.driveId && drives.value.find((drive) => drive.id === route.driveId);
  if (
    route.view === 'drives' &&
    restored &&
    readiness(restored).ready &&
    (activeId.value !== restored.id || (narrow.value && !mobileDetail.value))
  ) {
    void applyWorkspace(route);
    return;
  }
  if (!narrow.value && !activeId.value && view.value === 'drives')
    activeId.value =
      drives.value.find((drive) => readiness(drive).ready)?.id || '';
});
const segment = computed(() => active.value?.segments[segmentIndex.value]);
const connected = computed(() => demo.value || !!connectedAddress.value);
const deviceName = computed(() =>
  demo.value
    ? t('app.sampleDevice')
    : connectedAddress.value
      ? new URL(connectedAddress.value).host
      : t('app.noDeviceConnected'),
);
const bookmarkKey = (id: string) =>
  `${demo.value ? 'demo' : connectedAddress.value}|${id}`;
const isSaved = (id: string) => bookmarks.value.includes(bookmarkKey(id));
const visibleDrives = computed(() =>
  drives.value
    .filter((drive) => {
      const text =
        `${drive.id} ${dateLabel(drive.date)} ${timeLabel(drive.date)}`.toLowerCase();
      return (
        (filter.value === 'all' || isSaved(drive.id)) &&
        text.includes(query.value.toLowerCase())
      );
    })
    .sort((a, b) =>
      sort.value === 'oldest'
        ? a.date - b.date || a.id.localeCompare(b.id)
        : sort.value === 'longest'
          ? b.segments.length - a.segments.length
          : b.date - a.date || b.id.localeCompare(a.id),
    ),
);
const groups = computed(() => {
  const result: { label: string; drives: Drive[] }[] = [];
  for (const drive of visibleDrives.value) {
    const label = dateLabel(drive.date);
    if (result.at(-1)?.label !== label) result.push({ label, drives: [] });
    result.at(-1)!.drives.push(drive);
  }
  return result;
});
const playable = computed(() => {
  if (demo.value)
    return files.value.filter((file) =>
      /\.(mp4|webm|ts|hevc)$/.test(file.name),
    );
  const available = new Map<string, Entry>();
  for (const part of active.value?.segments || []) {
    for (const file of indexEntries.value[part.url]?.files || []) {
      if (
        /\.(mp4|webm|ts|hevc)$/.test(file.name) &&
        file.size > 0 &&
        (file.duration || 0) > 0
      )
        available.set(file.name, file);
    }
  }
  return [...available.values()].sort(compareCameras);
});
function cameraPreparing(name: string) {
  return (
    !demo.value &&
    name.endsWith('.hevc') &&
    (active.value?.segments || []).some((part) => {
      const file = indexEntries.value[part.url]?.files.find(
        (file) => file.name === name,
      );
      return file && file.size > 0 && !file.native;
    })
  );
}
const pendingNativeCamera = computed(
  () => playable.value.find((file) => cameraPreparing(file.name))?.name || '',
);
const totalSegments = computed(() =>
  drives.value.reduce((sum, drive) => sum + drive.segments.length, 0),
);
function dateLabel(date: number) {
  if (!date) return t('app.dateUnavailable');
  return new Date(date).toLocaleDateString(locale.value, {
    month: 'long',
    day: 'numeric',
    year: 'numeric',
  });
}
function timeLabel(date: number) {
  if (!date) return '—';
  return new Date(date).toLocaleTimeString(locale.value, {
    hour: 'numeric',
    minute: '2-digit',
  });
}
function tell(message: string) {
  notifications.show({ message, tone: 'info' });
}
watch(retainedLiveCapture, (clip) => {
  if (clip?.size) tell(t('app.liveClipReadySaveItFromLive'));
});
function saveDrive() {
  if (!active.value) return;
  const key = bookmarkKey(active.value.id);
  const exists = bookmarks.value.includes(key);
  bookmarks.value = exists
    ? bookmarks.value.filter((item) => item !== key)
    : [...bookmarks.value, key];
  const stored = writeStored('dash.bookmarks', bookmarks.value);
  tell(
    !stored
      ? t('app.savedForThisSessionBrowserStorageIs')
      : exists
        ? t('app.removedFromSavedDrives')
        : t('app.driveBookmarkedOnThisBrowser'),
  );
}
function selectDrive(drive: Drive) {
  if (!readiness(drive).ready) return;
  rememberScroll();
  sessionPane.value = 'video';
  workspaceHistory.navigate({
    view: 'drives',
    driveId: drive.id,
    returnView: view.value === 'overview' ? 'overview' : 'drives',
  });
}
async function loadFiles() {
  fileRequest?.abort();
  files.value = [];
  selectedSource.value = '';
  fileError.value = '';
  if (!segment.value || !active.value) {
    filesBusy.value = false;
    return;
  }
  if (demo.value) {
    files.value = demoFiles;
    selectedSource.value = demoFiles[0].url;
    filesBusy.value = false;
    return;
  }
  const request = new AbortController();
  fileRequest = request;
  filesBusy.value = true;
  try {
    const listing = await listFolder(segment.value.url, request.signal);
    if (request.signal.aborted) return;
    files.value = listing.files;
    const preview = preferredCamera(
      playable.value.filter(
        (file) =>
          !file.name.endsWith('.hevc') ||
          (active.value?.segments || []).every((part) => {
            const source = indexEntries.value[part.url]?.files.find(
              (candidate) => candidate.name === file.name,
            );
            return (
              !source || source.size <= 0 || source.native?.status === 'ready'
            );
          }),
      ),
    );
    selectedSource.value =
      preview && active.value
        ? recordingAddress(active.value.id, preview.name)
        : '';
  } catch (error) {
    if (!request.signal.aborted) fileError.value = (error as Error).message;
  } finally {
    if (!request.signal.aborted) filesBusy.value = false;
  }
}
watch([() => active.value?.id, segmentIndex], loadFiles);
watch(activeId, () => {
  playbackTime.value = 0;
  playbackDuration.value = 0;
});
let metadataRequest: AbortController | undefined;
async function loadMetadata() {
  metadataRequest?.abort();
  if (
    demo.value ||
    !active.value ||
    view.value !== 'drives' ||
    (narrow.value && !mobileDetail.value)
  )
    return;
  const drive = active.value;
  const request = new AbortController();
  metadataRequest = request;
  try {
    const response = await fetch(
      `/api/metadata?drive=${encodeURIComponent(drive.id)}`,
      { signal: request.signal },
    );
    if (!response.ok) return;
    const entries = await response.json();
    if (request.signal.aborted) return;
    for (const segment of drive.segments) {
      const name = decodeURIComponent(
        new URL(segment.url).pathname.split('/').filter(Boolean).at(-1)!,
      );
      if (entries[name] && indexEntries.value[segment.url])
        indexEntries.value[segment.url].metadata = entries[name].metadata;
    }
  } catch {
    /* Retain summary metadata while reconnecting. */
  }
}
watch(
  [
    activeId,
    view,
    mobileDetail,
    narrow,
    () =>
      active.value?.segments
        .map((part) => indexEntries.value[part.url]?.fingerprint)
        .join('|'),
    // The initial metadata request can finish before the index snapshot.
    // Retry once its target entry exists, including when workers are paused.
    () =>
      !!(
        active.value && indexEntries.value[active.value.segments[0]?.url || '']
      ),
  ],
  loadMetadata,
);
async function connect() {
  connectionRequest?.abort();
  const request = new AbortController();
  connectionRequest = request;
  busy.value = true;
  connectionError.value = '';
  try {
    const normalized = address.value;
    if (
      location.protocol === 'https:' &&
      new URL(normalized).protocol === 'http:'
    )
      throw new Error(t('app.thisAppIsUsingHttpsUseAn'));
    const listing = await listFolder(normalized, request.signal);
    if (request.signal.aborted) return;
    fileRequest?.abort();
    demo.value = false;
    connectedAddress.value = normalized;
    address.value = normalized;
    const previousId = activeId.value;
    listedDrives.value = groupDrives(listing.directories);
    activeId.value = drives.value.some(
      (item) => item.id === previousId && readiness(item).ready,
    )
      ? previousId
      : !narrow.value
        ? drives.value.find((drive) => readiness(drive).ready)?.id || ''
        : '';
    segmentIndex.value = 0;
    await applyWorkspace(workspaceHistory.current);
    backendOrigin.value = location.origin;
    indexDrives();
    connectionDialog.value?.close();
    await loadFiles();
  } catch (error) {
    if (!request.signal.aborted)
      connectionError.value =
        error instanceof TypeError
          ? t('app.openDashFromAValidDashBackend')
          : (error as Error).message;
  } finally {
    if (!request.signal.aborted) busy.value = false;
  }
}
function explore() {
  connectionRequest?.abort();
  fileRequest?.abort();
  stopIndexing();
  busy.value = false;
  backendOrigin.value = '';
  workspaceHistory.navigate({ view: 'drives' }, true);
  demo.value = true;
  connectedAddress.value = '';
  listedDrives.value = demoDrives;
  activeId.value = demoDrives[0].id;
  segmentIndex.value = 0;
  mobileDetail.value = false;
  connectionError.value = '';
  query.value = '';
  filter.value = 'all';
  connectionDialog.value?.close();
  indexEntries.value = {};
  indexStatus.value = {
    running: false,
    completed: 0,
    total: 0,
    errors: 0,
    driving: 'unknown',
    current: '',
    priority: '',
  };
  loadFiles();
}
function disconnect() {
  stopIndexing();
  connectionRequest?.abort();
  fileRequest?.abort();
  busy.value = false;
  filesBusy.value = false;
  connectedAddress.value = '';
  demo.value = false;
  workspaceHistory.navigate({ view: 'drives' }, true);
  listedDrives.value = [];
  activeId.value = '';
  files.value = [];
  mobileDetail.value = false;
  connectionError.value = '';
  connectionDialog.value?.close();
}
async function openConnection() {
  connectionError.value = '';
  connectionDialog.value?.showModal();
  await nextTick();
}
function cancelConnection() {
  connectionRequest?.abort();
  busy.value = false;
}
function updateOnline() {
  online.value = navigator.onLine;
}
onMounted(() => {
  void connect();
  window.addEventListener('online', updateOnline);
  window.addEventListener('offline', updateOnline);
});
function dismissTipOnEscape(event: KeyboardEvent) {
  if (event.key === 'Escape') hideTip();
}
onMounted(() => {
  window.addEventListener('resize', hideTip);
  window.addEventListener('scroll', hideTip, true);
  window.addEventListener('keydown', dismissTipOnEscape);
});
onBeforeUnmount(() => {
  workspaceHistory.dispose();
  mobileViewport.removeEventListener('change', viewportChanged);
  hideTip();
  window.removeEventListener('resize', hideTip);
  window.removeEventListener('scroll', hideTip, true);
  window.removeEventListener('keydown', dismissTipOnEscape);
  metadataRequest?.abort();
  stopIndexing();
  connectionRequest?.abort();
  fileRequest?.abort();

  window.removeEventListener('online', updateOnline);
  window.removeEventListener('offline', updateOnline);
});
</script>

<template>
  <div
    class="app-shell"
    :class="{ 'session-shell': connected && view === 'drives' && !!active }"
  >
    <SimulationControls />
    <HoverTip />
    <header class="topbar" :data-connected="connected || undefined">
      <div class="toolbar-leading">
        <button
          v-if="connected"
          type="button"
          ref="mobileMenu"
          class="icon-button mobile-menu"
          :aria-label="t('app.openNavigation')"
          aria-haspopup="dialog"
          @click="navigationDrawer?.showModal()"
        >
          <Menu :size="21" />
        </button>
        <a
          class="brand"
          href="#"
          @click.prevent="navigate('drives')"
          :aria-label="t('app.dashHome')"
          ><span class="brand-mark" aria-hidden="true"
            ><span></span><span></span></span
          ><span class="brand-name">dash</span></a
        >
      </div>
      <span v-if="connected" class="mobile-page-title">{{
        view === 'drives'
          ? t('app.recordings')
          : view === 'overview'
            ? t('app.overview')
            : t('app.live')
      }}</span>
      <div
        class="header-actions"
        role="group"
        :aria-label="t('app.appControls')"
      >
        <SettingsPopup
          @rebuild="indexDrives(true)"
          @help="showHelp"
          @transfers="transfersDialog?.open('destinations')"
        />
        <button
          type="button"
          class="icon-button desktop-help"
          :aria-label="t('app.helpAndInstallation')"
          v-tip="t('app.help')"
          data-tip-placement="below"
          @click="showHelp"
        >
          <CircleHelp :size="19" />
        </button>
      </div>
    </header>

    <dialog
      ref="navigationDrawer"
      class="navigation-drawer"
      :aria-label="t('app.navigation')"
      @click="$event.target === navigationDrawer && navigationDrawer?.close()"
    >
      <div class="drawer-heading">
        <strong>dash</strong
        ><button
          class="icon-button"
          :aria-label="t('app.closeNavigation')"
          @click="navigationDrawer?.close()"
        >
          <X :size="20" />
        </button>
      </div>
      <WorkspaceNavigation
        :view="view"
        :device-name="deviceName"
        @navigate="navigate"
        @device="switchDevice"
      />
    </dialog>
    <main v-if="!connected" class="welcome">
      <div class="welcome-copy">
        <div class="eyebrow">
          <span class="status-dot"></span> {{ t('app.localRecordings') }}
        </div>
        <h1>{{ t('app.reviewYourRecordings') }}</h1>
        <p>
          {{ t('app.yourRecordingsStraightFromYourCommaDevice')
          }}<br class="desktop-only" />
          {{ t('app.browseCameraFootageAndExportCompleteSessions') }}
        </p>
        <button class="button primary large" @click="connect">
          {{ busy ? t('app.connecting') : t('app.openYourDevice') }}
          <ArrowRight :size="18" />
        </button>
        <p v-if="connectionError" class="welcome-error" role="alert">
          {{ connectionError }} {{ t('app.startDashBackendAndOpenItsAddress') }}
        </p>
        <button class="text-button sample-link" @click="explore">
          {{ t('app.openSampleLibrary') }} <ArrowUpRight :size="16" />
        </button>
        <div class="welcome-assurances">
          <span><Check :size="14" /> {{ t('app.noAccount') }}</span
          ><span><Check :size="14" /> {{ t('app.noCloudUploads') }}</span
          ><span><Check :size="14" /> {{ t('app.justYourNetwork') }}</span>
        </div>
      </div>
      <div
        class="welcome-preview"
        :aria-label="t('app.illustratedDriveLibraryPreview')"
      >
        <div class="preview-top">
          <span class="status-dot"></span> {{ t('app.sampleRecording') }}
          <span class="preview-dots">•••</span>
        </div>
        <img
          src="/sample-road.svg"
          :alt="t('app.illustrationOfAWindingRoadBetweenGreen')"
        />
        <div class="preview-bottom">
          <div>
            <span class="eyebrow">{{ t('app.sampleLibrary') }}</span>
            <h3>{{ t('app.roadCamera') }}</h3>
            <p>{{ t('app.illustratedSampleFootage') }}</p>
          </div>
          <span class="preview-play"><ArrowUpRight :size="25" /></span>
        </div>
        <span class="floating-note"
          ><ShieldCheck :size="17" /> {{ t('app.localDeviceConnection') }}</span
        >
      </div>
      <div class="welcome-foot">
        <span>{{ t('app.recordingsStayOnYourDevice') }}</span
        ><span
          >{{ t('app.servedByDashBackend') }}
          <span class="tiny-separator">/</span>
          {{ t('app.openpilotRecordings') }}</span
        >
      </div>
    </main>

    <main
      v-else
      class="workspace"
      :class="{ 'show-detail': mobileDetail }"
      :style="{ '--sidebar-width': `${sidebarWidth}px` }"
      :data-sidebar-resetting="sidebarResetting || undefined"
    >
      <aside class="sidebar">
        <div class="desktop-sidebar-nav">
          <div class="sidebar-brand-row">
            <a
              class="brand"
              href="#"
              :aria-label="t('app.dashHome')"
              @click.prevent="navigate('drives')"
            >
              <span class="brand-mark" aria-hidden="true"
                ><span></span><span></span></span
              ><span class="brand-name">dash</span>
            </a>
            <div
              class="sidebar-utilities"
              role="group"
              :aria-label="t('app.appControls')"
            >
              <button
                type="button"
                class="icon-button utility-button"
                :aria-label="t('app.helpAndInstallation')"
                v-tip="t('app.help')"
                data-tip-placement="below"
                @click="showHelp"
              >
                <CircleHelp :size="18" />
              </button>
              <SettingsPopup
                @rebuild="indexDrives(true)"
                @help="showHelp"
                @transfers="transfersDialog?.open('destinations')"
              />
            </div>
          </div>
          <DeviceSwitcher
            class="sidebar-device"
            :name="deviceName"
            @switch="switchDevice"
          />
          <WorkspaceNavigation
            :view="view"
            :device-name="deviceName"
            :show-device="false"
            @navigate="navigate"
          />
        </div>
        <div v-show="view === 'drives'" class="sidebar-library">
          <div class="sidebar-heading">
            <h2>
              {{ t('app.yourDrives')
              }}<span
                :aria-label="
                  t('app.driveCount', {
                    visible: visibleDrives.length,
                    total: drives.length,
                  })
                "
                >{{
                  query || filter === 'saved'
                    ? `${formatNumber(visibleDrives.length)} / ${formatNumber(drives.length)}`
                    : formatNumber(drives.length)
                }}</span
              >
            </h2>
            <button
              v-if="backendOrigin"
              class="icon-button"
              :aria-label="t('transfers.exportRecordings')"
              v-tip="t('transfers.exportRecordings')"
              @click="transfersDialog?.open()"
            >
              <Download :size="17" />
            </button>
            <button
              class="icon-button"
              :disabled="busy"
              :aria-label="t('app.refreshDrives')"
              @click="
                demo ? tell(t('app.youreViewingTheSampleLibrary')) : connect()
              "
            >
              <RefreshCw :size="17" :class="{ spinning: busy }" />
            </button>
          </div>
          <label class="search"
            ><Search :size="16" /><input
              v-model="query"
              type="search"
              :placeholder="t('app.findADateOrDrive')"
              :aria-label="t('app.searchDrives')"
          /></label>
          <div v-if="connectionError" class="inline-error" role="alert">
            {{ connectionError
            }}<button class="text-button" @click="openConnection">
              {{ t('app.checkConnection') }} <ArrowRight :size="14" />
            </button>
          </div>
          <div
            class="library-tools"
            role="group"
            :aria-label="t('app.driveListControls')"
          >
            <button
              type="button"
              class="saved-filter"
              :aria-label="t('app.savedDrivesOnly')"
              :aria-pressed="filter === 'saved'"
              :title="
                filter === 'saved'
                  ? t('app.showAllDrives')
                  : t('app.showSavedDrivesOnly')
              "
              @click="filter = filter === 'saved' ? 'all' : 'saved'"
            >
              <Bookmark
                :size="14"
                :fill="filter === 'saved' ? 'currentColor' : 'none'"
              />
              {{ t('app.saved') }}
            </button>
            <SelectMenu
              v-model="sort"
              class="library-sort"
              :aria-label="t('app.sortDrives')"
              :options="
                [
                  { value: 'newest', label: t('app.newestFirst') },
                  { value: 'oldest', label: t('app.oldestFirst') },
                  { value: 'longest', label: t('app.mostSegments') },
                ] as const
              "
            />
          </div>
          <div class="drive-scroll">
            <div v-for="group in groups" :key="group.label" class="drive-group">
              <h3>{{ group.label }}</h3>
              <div
                v-for="drive in group.drives"
                :key="drive.id"
                class="drive-item"
                :data-state="
                  readiness(drive).ready
                    ? 'ready'
                    : readiness(drive).empty
                      ? 'empty'
                      : 'pending'
                "
              >
                <button
                  class="drive-row"
                  :disabled="!readiness(drive).ready"
                  :class="{
                    active: drive.id === activeId && readiness(drive).ready,
                  }"
                  :aria-current="
                    drive.id === activeId && readiness(drive).ready
                      ? 'true'
                      : undefined
                  "
                  @click="selectDrive(drive)"
                >
                  <span class="drive-glyph"
                    ><img
                      v-if="thumbnail(drive)"
                      :src="thumbnail(drive)"
                      alt=""
                      loading="lazy" /><ListVideo v-else :size="20" /></span
                  ><span class="drive-row-text"
                    ><strong
                      >{{ recordingName(drive) }}
                      <Bookmark
                        v-if="isSaved(drive.id)"
                        :size="12"
                        fill="currentColor" /></strong
                    ><small
                      >{{ readinessLabel(drive) }}
                      <span v-if="readiness(drive).ready">·</span>
                      {{
                        !readiness(drive).ready
                          ? ''
                          : drive.dateEstimated
                            ? t('app.estimatedDate')
                            : drive.dated
                              ? t('app.recordedDrive')
                              : t('app.dateUnavailable')
                      }}</small
                    ></span
                  ><ChevronRight
                    v-if="readiness(drive).ready"
                    :size="15"
                  /><LoaderCircle
                    v-else-if="indexStatus.current?.startsWith(drive.id + '--')"
                    class="spinning"
                    :size="16"
                  />
                </button>
                <button
                  v-if="!readiness(drive).ready && !readiness(drive).empty"
                  class="prepare-next icon-button"
                  :disabled="
                    indexStatus.driving !== 'parked' ||
                    indexStatus.priority === drive.id
                  "
                  :aria-label="
                    indexStatus.priority === drive.id
                      ? t('app.queuedNext')
                      : t('app.prepareNext')
                  "
                  v-tip="
                    indexStatus.priority === drive.id
                      ? t('app.queuedNext')
                      : t('app.prepareNext')
                  "
                  @click="prepareNext(drive)"
                >
                  <Check
                    v-if="indexStatus.priority === drive.id"
                    :size="16"
                  /><ArrowUpRight v-else :size="17" />
                </button>
              </div>
            </div>
            <div v-if="!visibleDrives.length" class="empty-list">
              <Search :size="25" />
              <h3>
                {{
                  query
                    ? t('app.noMatchingDrives')
                    : filter === 'saved'
                      ? t('app.aPlaceForYourFavorites')
                      : t('app.noDrivesHereYet')
                }}
              </h3>
              <p>
                {{
                  query
                    ? t('app.tryAnotherDateOrClearYourSearch')
                    : filter === 'saved'
                      ? t('app.bookmarkADriveToFindItHere')
                      : t('app.yourDeviceIsConnectedWhenRecordingsAppear')
                }}
              </p>
              <button
                v-if="query || filter === 'saved'"
                class="text-button"
                @click="
                  query = '';
                  filter = 'all';
                "
              >
                {{ t('app.showAllDrives') }}</button
              ><button v-else class="text-button" @click="openConnection">
                {{ t('app.deviceSettings') }}
              </button>
            </div>
          </div>
        </div>
        <Transition name="index-activity">
          <div
            v-if="
              !demo && indexStatus.running && indexStatus.driving === 'parked'
            "
            class="index-activity"
          >
            <div>
              <div class="index-footer" role="status" aria-live="polite">
                <LoaderCircle class="spinning" :size="14" />
                <span>{{
                  t('app.indexProgress', {
                    completed: indexStatus.completed,
                    total: indexStatus.total,
                  })
                }}</span>
              </div>
            </div>
          </div>
        </Transition>
      </aside>
      <SidebarResize
        v-model="sidebarWidth"
        v-model:resetting="sidebarResetting"
      />
      <section
        class="detail"
        :class="{
          'session-view': view === 'drives' && !!active,
          'live-detail': view === 'live' && !demo,
        }"
      >
        <div v-if="demo" class="demo-banner">
          <span
            ><span class="status-dot amber"></span>
            {{ t('app.sampleRecordings') }}</span
          ><button @click="openConnection">
            {{ t('app.connectYourDevice') }} <ArrowRight :size="14" />
          </button>
        </div>
        <div v-if="!online && !demo" class="offline-banner" role="status">
          {{ t('app.youreOfflineTheAppIsAvailableBut') }}
        </div>
        <button
          v-if="view === 'overview'"
          class="mobile-back text-button"
          @click="navigate('drives')"
        >
          <ArrowLeft :size="16" /> {{ t('app.recordings') }}
        </button>
        <KeepAlive>
          <RecordingDashboard
            v-if="view === 'overview'"
            :key="connectedAddress"
            v-model="dashboardSelection"
            :drives="drives"
            :entries="indexEntries"
            :demo="demo"
            @open="selectDrive"
          />
        </KeepAlive>
        <LiveView v-if="view === 'live' && !demo" />
        <div v-else-if="view === 'live'" class="no-selection">
          <Wifi :size="35" />
          <h1>{{ t('app.liveCameras') }}</h1>
          <p>
            {{ t('app.liveViewNeedsDashBackendAndSupported') }}
          </p>
        </div>
        <template v-else-if="view === 'drives' && active">
          <div class="detail-heading">
            <button
              class="mobile-back icon-button session-return"
              :aria-label="backLabel"
              @click="returnFromDrive"
            >
              <ArrowLeft :size="20" />
            </button>
            <div>
              <div class="eyebrow">
                {{ demo ? t('app.sampleDrive') : t('app.driveReview') }}
              </div>
              <h1>{{ dateLabel(active.date) }}</h1>
              <p>
                {{ timeLabel(active.date) }}
                <span v-if="!active.dated"
                  >· {{ t('app.dateUnavailable') }}</span
                ><span v-else-if="active.dateEstimated"
                  >· {{ t('app.estimatedDate') }}</span
                >
              </p>
            </div>
            <SessionActions ref="actionMenu">
              <button
                v-if="pendingNativeCamera"
                type="button"
                :disabled="
                  indexStatus.driving !== 'parked' ||
                  indexStatus.priority === active.id
                "
                @click="
                  prepareNext(active, pendingNativeCamera);
                  actionMenu?.close();
                "
              >
                <ArrowUpRight :size="16" />
                {{
                  indexStatus.priority === active.id
                    ? t('app.queuedNext')
                    : t('player.prepareHighResolution')
                }}
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('mp4');
                "
              >
                {{ t('app.downloadMp4') }}
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('mcap');
                "
              >
                {{ t('app.downloadMcap') }}
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('raw');
                "
              >
                {{ t('app.downloadRawVideo') }}
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('zip');
                "
              >
                {{ t('app.downloadZip') }}
              </button>
              <button
                @click="
                  actionMenu?.close();
                  originalFilesDialog?.showModal();
                "
              >
                {{ t('app.originalFiles') }}
              </button>
              <button
                @click="
                  saveDrive();
                  actionMenu?.close();
                "
              >
                {{
                  isSaved(active.id)
                    ? t('app.removeBookmark')
                    : t('app.bookmarkDrive')
                }}
              </button>
            </SessionActions>
          </div>
          <ExportDialog
            ref="exportDialog"
            :drive="active"
            :files="playable"
            :sample="demo"
          />
          <Sheet
            ref="originalFilesDialog"
            :title="t('app.originalFiles')"
            :close-label="t('app.closeOriginalFiles')"
            wide
            class="session-info-dialog"
          >
            <div class="modal-inner">
              <section class="files-section">
                <div class="section-heading">
                  <div>
                    <p>
                      {{
                        t('app.originalFilesHint', {
                          segment: (segment?.index ?? 0) + 1,
                        })
                      }}
                    </p>
                  </div>
                </div>
                <div class="file-grid">
                  <a
                    v-for="file in files"
                    :key="file.url"
                    class="file-card"
                    :href="demo ? file.url : downloadUrl(file.url)"
                    :download="file.name"
                    ><span class="file-icon"
                      ><ListVideo
                        v-if="/\.(hevc|ts|mp4|webm)$/.test(file.name)"
                        :size="20" /><FolderOpen v-else :size="20" /></span
                    ><span
                      ><strong>{{ file.name }}</strong
                      ><small>{{
                        demo
                          ? t('app.illustratedSampleMp4')
                          : t('app.originalFileSize', {
                              size: formatBytes(file.size),
                            })
                      }}</small></span
                    ><Download :size="17"
                  /></a>
                </div>
                <p
                  v-if="!files.length && !filesBusy && !fileError"
                  class="muted"
                >
                  {{ t('app.noFilesInThisSegmentItMay') }}
                </p>
              </section>
              <p class="muted">
                {{ t('app.bookmarksDoNotProtectRecordingsFromDevice') }}
              </p>
            </div>
          </Sheet>
          <div
            v-glass-tabs
            class="session-mobile-tabs segmented"
            :aria-label="t('app.sessionPanels')"
          >
            <button
              :class="{ selected: sessionPane === 'video' }"
              :aria-pressed="sessionPane === 'video'"
              @click="sessionPane = 'video'"
            >
              {{ t('app.video') }}
            </button>
            <button
              :class="{ selected: sessionPane === 'data' }"
              :aria-pressed="sessionPane === 'data'"
              @click="sessionPane = 'data'"
            >
              {{ t('app.mapData') }}
            </button>
          </div>
          <ResizablePanels :class="{ 'show-data': sessionPane === 'data' }">
            <template #video>
              <div class="session-video">
                <div class="viewer-card">
                  <div
                    v-if="filesBusy"
                    class="player loading-player"
                    role="status"
                  >
                    <LoaderCircle class="spinning" :size="28" /><span>{{
                      t('app.loadingRecording')
                    }}</span>
                  </div>
                  <div v-else-if="fileError" class="player loading-player">
                    <CircleHelp :size="30" />
                    <p role="alert">{{ fileError }}</p>
                    <button class="button" @click="loadFiles">
                      {{ t('app.tryAgain') }}
                    </button>
                  </div>
                  <RecordingPlayer
                    v-else
                    :key="active.id"
                    :source="selectedSource || undefined"
                    :sample="demo"
                    :segments="timelineSegments"
                    :controls-target="transportTarget"
                    @fullscreen-requested="sessionPane = 'video'"
                    @positionframe="playbackTime = $event"
                    @timeupdate="
                      (time, duration) => {
                        playbackTime = time;
                        playbackDuration = duration;
                      }
                    "
                  >
                    <template #camera>
                      <label v-if="playable.length > 1" class="source-picker"
                        >{{ t('app.camera') }}
                        <SelectMenu
                          v-model="selectedSource"
                          :aria-label="t('app.camera')"
                          :options="
                            playable.map((file) => ({
                              value: demo
                                ? file.url
                                : recordingAddress(activeId, file.name),
                              label: cameraPreparing(file.name)
                                ? `${file.name} · ${indexStatus.driving === 'parked' ? t('player.indexingCamera') : t('player.parkToPrepare')}`
                                : file.name,
                              disabled: cameraPreparing(file.name),
                            }))
                          "
                      /></label>
                    </template>
                  </RecordingPlayer>
                </div>
              </div>
            </template>
            <template #data>
              <aside class="session-data">
                <section v-if="driveMetadata.length" class="trip-details">
                  <SessionFacts
                    class="desktop-session-facts"
                    :metadata="driveMetadata"
                    :total="active.segments.length"
                  />
                  <button
                    type="button"
                    class="mobile-trip-summary"
                    :aria-label="t('app.tripDetails')"
                    @click="tripDetails?.showModal()"
                  >
                    <span>{{ t('app.tripDetails') }}</span
                    ><span
                      >{{
                        formatNumber(
                          driveMetadata.reduce(
                            (sum, meta) => sum + (meta?.duration || 0),
                            0,
                          ) / 60,
                          {
                            maximumFractionDigits: 1,
                            minimumFractionDigits: 1,
                          },
                        )
                      }}
                      {{ t('facts.min') }} ·
                      {{
                        t('common.coveredParts', {
                          covered: driveMetadata.length,
                          total: active.segments.length,
                        })
                      }}
                      <ChevronRight :size="14"
                    /></span>
                  </button>
                  <Sheet
                    ref="tripDetails"
                    :title="t('app.tripDetails')"
                    :close-label="t('app.closeTripDetails')"
                    class="trip-facts-sheet"
                    ><div class="modal-inner">
                      <SessionFacts
                        :metadata="driveMetadata"
                        :total="active.segments.length"
                      /></div
                  ></Sheet>
                  <TripMap
                    :key="active.id"
                    v-if="drivePoints.length"
                    :points="drivePoints"
                    :position="playbackPosition"
                  />
                  <p v-else class="muted">
                    {{ t('app.noValidGpsFixesInTheIndexed') }}
                  </p>
                </section>
                <template v-if="!driveMetadata.length"
                  ><h2>{{ t('app.metadata') }}</h2>
                  <p>
                    {{ t('app.metadataIsNotAvailableYetIndexingRuns') }}
                  </p></template
                >
              </aside>
            </template>
          </ResizablePanels>
          <div
            ref="mobilePlaybackDock"
            class="mobile-playback-dock"
            :aria-label="t('app.recordingPlaybackControls')"
          ></div>
        </template>
        <div v-else-if="view === 'drives'" class="no-selection">
          <HardDrive :size="42" />
          <h1>{{ t('app.selectADrive') }}</h1>
          <p>
            {{
              totalSegments
                ? drives.some((drive) => readiness(drive).ready)
                  ? t('app.chooseADriveFromTheLibrary')
                  : t('app.recordingsWillUnlock')
                : t('app.yourDeviceIsConnectedWhenRecordingsAppear')
            }}
          </p>
          <button class="button" :disabled="busy" @click="connect">
            {{ t('app.refreshRecordings') }}
          </button>
        </div>
        <footer v-if="view === 'overview'" class="detail-footer">
          <span>{{ t('app.localRecordings') }}</span
          ><span
            >dash <span class="footer-dot">·</span>
            {{ t('app.localAccess') }}</span
          >
        </footer>
      </section>
    </main>

    <Sheet
      ref="connectionDialog"
      :title="t('app.deviceSettings')"
      :close-label="t('app.closeDeviceSettings')"
      @close="cancelConnection"
    >
      <div class="modal-inner">
        <div class="eyebrow">{{ t('app.dashBackend') }}</div>

        <p>
          {{ t('app.dashIsServedByDashBackendOn') }}
        </p>
        <div class="backend-address">{{ appOrigin }}</div>
        <div v-if="connectionError" class="inline-error" role="alert">
          {{ connectionError }}
        </div>
        <button
          class="button primary full-width"
          :disabled="busy"
          @click="connect"
        >
          {{ busy ? t('app.connecting') : t('app.reconnectToDevice') }}</button
        ><button v-if="demo" class="text-button centered" @click="disconnect">
          {{ t('app.leaveSampleLibrary') }}
        </button>
        <DiagnosticsDownload :available="!!backendOrigin" />
      </div>
    </Sheet>

    <Sheet
      ref="helpDialog"
      :title="t('app.help')"
      :close-label="t('app.closeHelp')"
    >
      <div class="modal-inner">
        <p>
          {{ t('app.dashReadsRecordingsThroughDashBackendOn') }}
        </p>
        <h3>{{ t('app.installDash') }}</h3>
        <p>
          {{ t('app.onIphoneOrIpadOpenDashIn') }}
        </p>
        <h3>{{ t('app.offlineAccess') }}</h3>
        <p>
          {{ t('app.theAppShellWorksOfflineAfterYour') }}
        </p>
        <h3>{{ t('app.processingOnYourDevice') }}</h3>
        <p>
          {{ t('app.backgroundIndexingAndVideoProcessingRunOnly') }}
        </p>
        <p>
          {{ t('app.rebuildTheRecordingIndexInSettingsIf') }}
        </p>
        <h3>{{ t('app.aboutRecordings') }}</h3>
        <p>
          {{ t('app.dashPlaysMp4WebmAndSupportedH') }}
        </p>
        <button class="button primary full-width" @click="helpDialog?.close()">
          {{ t('app.gotIt') }} <Check :size="17" />
        </button>
      </div>
    </Sheet>
    <TransfersSheet ref="transfersDialog" />
    <ToastStack :centred="!connected" />
  </div>
</template>
