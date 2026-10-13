<script setup lang="ts">
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
import Sheet from './components/Sheet.vue';
import SelectMenu from './components/SelectMenu.vue';
import SessionActions from './components/SessionActions.vue';
import RecordingPlayer from './components/RecordingPlayer.vue';
import ResizablePanels from './components/ResizablePanels.vue';
import ExportDialog from './components/ExportDialog.vue';
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
  distanceKm,
} from './indexer';
import {
  downloadUrl,
  formatBytes,
  groupDrives,
  listFolder,
  type Drive,
  type Entry,
} from './library';
import { demoDrives, demoFiles } from './demo';
import { readStored, writeStored } from './storage';

const appOrigin = location.origin;
const address = ref(new URL('/files/', location.origin).href);
const connectedAddress = ref('');
const demo = ref(false);
const view = ref<'drives' | 'overview' | 'live'>('drives');
const navigationDrawer = ref<HTMLDialogElement>();
function navigate(destination: 'drives' | 'overview' | 'live') {
  view.value = destination;
  mobileDetail.value = destination !== 'drives';
  navigationDrawer.value?.close();
}
function switchDevice() {
  navigationDrawer.value?.close();
  openConnection();
}
const sort = ref<'newest' | 'oldest' | 'longest'>('newest');
const exportDialog = ref<InstanceType<typeof ExportDialog>>();
const drivePoints = computed(() =>
  (
    active.value?.segments.flatMap(
      (segment) => indexEntries.value[segment.url]?.metadata?.points || [],
    ) || []
  ).sort((a, b) => a.time - b.time),
);
const driveMetadata = computed(
  () =>
    active.value?.segments
      .map((segment) => indexEntries.value[segment.url]?.metadata)
      .filter((meta) => meta?.status === 'ready') || [],
);
const statistics = computed(() =>
  drives.value.map((drive) => {
    const meta = drive.segments
      .map((segment) => indexEntries.value[segment.url]?.metadata)
      .filter((entry) => entry?.status === 'ready');
    const points = meta
      .flatMap((entry) => entry?.points || [])
      .sort((a, b) => a.time - b.time);
    return {
      id: drive.id,
      date: drive.date,
      distance: meta.reduce((sum, entry) => sum + (entry?.distanceKm || 0), 0),
      duration: meta.reduce((sum, entry) => sum + (entry?.duration || 0), 0),
      covered: meta.length,
      total: drive.segments.length,
    };
  }),
);
const totals = computed(() => ({
  distance: statistics.value.reduce((sum, drive) => sum + drive.distance, 0),
  duration: statistics.value.reduce((sum, drive) => sum + drive.duration, 0),
  covered: statistics.value.reduce((sum, drive) => sum + drive.covered, 0),
}));
const thumbnail = (drive: Drive) =>
  demo.value
    ? '/sample-road.svg'
    : drive.segments
        .map((segment) => indexEntries.value[segment.url]?.thumbnail)
        .find(Boolean);
function indexDrives(rebuild = false) {
  if (demo.value) {
    tell('Sample library is already indexed.');
    return;
  }
  void startIndexing(connectedAddress.value, drives.value, rebuild).catch(
    (error) => tell((error as Error).message),
  );
}

const drives = ref<Drive[]>([]);
const activeId = ref('');
const segmentIndex = ref(0);
const files = ref<Entry[]>([]);
const selectedSource = ref('');
const busy = ref(false);
const filesBusy = ref(false);
const connectionError = ref('');
const fileError = ref('');
const notice = ref('');
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
let toastTimer: ReturnType<typeof setTimeout>;
const active = computed(() =>
  drives.value.find((drive) => drive.id === activeId.value),
);
const segment = computed(() => active.value?.segments[segmentIndex.value]);
const connected = computed(() => demo.value || !!connectedAddress.value);
const deviceName = computed(() =>
  demo.value
    ? 'Sample device'
    : connectedAddress.value
      ? new URL(connectedAddress.value).host
      : 'No device connected',
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
        ? a.date - b.date
        : sort.value === 'longest'
          ? b.segments.length - a.segments.length
          : b.date - a.date,
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
const playable = computed(() =>
  files.value.filter((file) => /\.(mp4|webm|ts|hevc)(?:\?|$)/i.test(file.name)),
);
const totalSegments = computed(() =>
  drives.value.reduce((sum, drive) => sum + drive.segments.length, 0),
);
function dateLabel(date: number) {
  return new Date(date).toLocaleDateString(undefined, {
    month: 'long',
    day: 'numeric',
    year: 'numeric',
  });
}
function timeLabel(date: number) {
  return new Date(date).toLocaleTimeString(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  });
}
function tell(message: string) {
  notice.value = message;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (notice.value = ''), 4500);
}
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
      ? 'Saved for this session. Browser storage is unavailable.'
      : exists
        ? 'Removed from saved drives.'
        : 'Drive bookmarked on this browser.',
  );
}
function selectDrive(drive: Drive) {
  sessionPane.value = 'video';
  view.value = 'drives';
  activeId.value = drive.id;
  segmentIndex.value = 0;
  mobileDetail.value = true;
}
async function loadFiles() {
  fileRequest?.abort();
  files.value = [];
  selectedSource.value = '';
  fileError.value = '';
  if (!segment.value) {
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
    const preview =
      listing.files.find((file) => file.name === 'qcamera.ts') ||
      playable.value[0];
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
watch([activeId, segmentIndex], loadFiles);
let metadataRequest: AbortController | undefined;
async function loadMetadata() {
  metadataRequest?.abort();
  if (demo.value || !active.value) return;
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
watch([activeId, () => indexStatus.value.completed], loadMetadata);
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
      throw new Error(
        'This app is using HTTPS. Use an HTTPS address for your device, or run Dash on localhost to connect to an HTTP device.',
      );
    const listing = await listFolder(normalized, request.signal);
    if (request.signal.aborted) return;
    fileRequest?.abort();
    demo.value = false;
    connectedAddress.value = normalized;
    address.value = normalized;
    drives.value = groupDrives(listing.directories);
    query.value = '';
    filter.value = 'all';
    activeId.value = drives.value[0]?.id || '';
    segmentIndex.value = 0;
    mobileDetail.value = false;
    backendOrigin.value = location.origin;
    indexDrives();
    connectionDialog.value?.close();
    await loadFiles();
  } catch (error) {
    if (!request.signal.aborted)
      connectionError.value =
        error instanceof TypeError
          ? 'Open Dash from a valid dash-backend address.'
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
  view.value = 'drives';
  demo.value = true;
  connectedAddress.value = '';
  drives.value = demoDrives;
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
  drives.value = [];
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
onBeforeUnmount(() => {
  metadataRequest?.abort();
  stopIndexing();
  connectionRequest?.abort();
  fileRequest?.abort();
  clearTimeout(toastTimer);
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
    <header class="topbar">
      <a
        class="brand"
        href="#"
        @click.prevent="mobileDetail = false"
        aria-label="Dash home"
        ><span class="brand-mark"><span></span><span></span></span>dash<span
          class="brand-divider"
        ></span
        ><span class="brand-caption">Local recordings</span></a
      >
      <button
        v-if="connected"
        class="icon-button mobile-menu"
        aria-label="Open navigation"
        aria-haspopup="dialog"
        @click="navigationDrawer?.showModal()"
      >
        <Menu :size="21" />
      </button>
      <WorkspaceNavigation
        v-if="connected"
        class="desktop-navigation"
        :view="view"
        :device-name="deviceName"
        @navigate="navigate"
        @device="switchDevice"
      />
      <div class="header-actions">
        <SettingsPopup />
        <span class="local-badge"
          ><ShieldCheck :size="14" /> Local recordings</span
        ><button
          class="icon-button"
          aria-label="Help and installation"
          @click="helpDialog?.showModal()"
        >
          <CircleHelp :size="19" />
        </button>
      </div>
    </header>

    <dialog
      ref="navigationDrawer"
      class="navigation-drawer"
      aria-label="Navigation"
      @click="$event.target === navigationDrawer && navigationDrawer?.close()"
    >
      <div class="drawer-heading">
        <strong>dash</strong
        ><button
          class="icon-button"
          aria-label="Close navigation"
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
          <span class="status-dot"></span> LOCAL RECORDINGS
        </div>
        <h1>Review your recordings.</h1>
        <p>
          Your recordings, straight from your comma device.<br
            class="desktop-only"
          />
          Browse camera footage and export complete sessions.
        </p>
        <button class="button primary large" @click="connect">
          {{ busy ? 'Connecting…' : 'Open your device' }}
          <ArrowRight :size="18" />
        </button>
        <p v-if="connectionError" class="welcome-error" role="alert">
          {{ connectionError }} Start dash-backend and open its address to use
          Dash.
        </p>
        <button class="text-button sample-link" @click="explore">
          Open sample library <ArrowUpRight :size="16" />
        </button>
        <div class="welcome-assurances">
          <span><Check :size="14" /> No account</span
          ><span><Check :size="14" /> No cloud uploads</span
          ><span><Check :size="14" /> Just your network</span>
        </div>
      </div>
      <div
        class="welcome-preview"
        aria-label="Illustrated drive library preview"
      >
        <div class="preview-top">
          <span class="status-dot"></span> SAMPLE RECORDING
          <span class="preview-dots">•••</span>
        </div>
        <img
          src="/sample-road.svg"
          alt="Illustration of a winding road between green coastal hills"
        />
        <div class="preview-bottom">
          <div>
            <span class="eyebrow">SAMPLE LIBRARY</span>
            <h3>Road camera</h3>
            <p>Illustrated sample footage.</p>
          </div>
          <span class="preview-play"><ArrowUpRight :size="25" /></span>
        </div>
        <span class="floating-note"
          ><ShieldCheck :size="17" /> Local device connection.</span
        >
      </div>
      <div class="welcome-foot">
        <span>Recordings stay on your device.</span
        ><span
          >Served by dash-backend
          <span class="tiny-separator">/</span> openpilot recordings</span
        >
      </div>
    </main>

    <main v-else class="workspace" :class="{ 'show-detail': mobileDetail }">
      <aside class="sidebar">
        <div class="sidebar-heading">
          <h2>
            Your drives<span>{{ drives.length }}</span>
          </h2>
          <button
            class="icon-button"
            :disabled="busy"
            aria-label="Refresh drives"
            @click="
              demo ? tell('You’re viewing the sample library.') : connect()
            "
          >
            <RefreshCw :size="17" :class="{ spinning: busy }" />
          </button>
        </div>
        <div v-glass-tabs class="segmented">
          <button
            :class="{ selected: filter === 'all' }"
            @click="filter = 'all'"
          >
            All drives</button
          ><button
            :class="{ selected: filter === 'saved' }"
            @click="filter = 'saved'"
          >
            <Bookmark :size="13" /> Saved
          </button>
        </div>
        <label class="search"
          ><Search :size="16" /><input
            v-model="query"
            type="search"
            placeholder="Find a date or drive…"
            aria-label="Search drives"
        /></label>
        <div v-if="connectionError" class="inline-error" role="alert">
          {{ connectionError
          }}<button class="text-button" @click="openConnection">
            Check connection <ArrowRight :size="14" />
          </button>
        </div>
        <label class="sort-picker"
          >Sort by<SelectMenu
            v-model="sort"
            aria-label="Sort drives"
            :options="
              [
                { value: 'newest', label: 'Newest first' },
                { value: 'oldest', label: 'Oldest first' },
                { value: 'longest', label: 'Most segments' },
              ] as const
            "
        /></label>
        <div class="drive-scroll">
          <div v-for="group in groups" :key="group.label" class="drive-group">
            <h3>{{ group.label }}</h3>
            <button
              v-for="drive in group.drives"
              :key="drive.id"
              class="drive-row"
              :class="{ active: drive.id === activeId }"
              :aria-current="drive.id === activeId ? 'true' : undefined"
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
                  >{{ timeLabel(drive.date) }}
                  <Bookmark
                    v-if="isSaved(drive.id)"
                    :size="12"
                    fill="currentColor" /></strong
                ><small
                  >{{ drive.segments.length }} segments <span>·</span>
                  {{ drive.dated ? 'Recorded drive' : 'File timestamp' }}</small
                ></span
              ><ChevronRight :size="15" />
            </button>
          </div>
          <div v-if="!visibleDrives.length" class="empty-list">
            <Search :size="25" />
            <h3>
              {{
                query
                  ? 'No matching drives'
                  : filter === 'saved'
                    ? 'A place for your favorites'
                    : 'No drives here yet'
              }}
            </h3>
            <p>
              {{
                query
                  ? 'Try another date or clear your search.'
                  : filter === 'saved'
                    ? 'Bookmark a drive to find it here later. Recordings stay on your device.'
                    : 'Choose the folder containing your openpilot segment directories, usually realdata.'
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
              Show all drives</button
            ><button v-else class="text-button" @click="openConnection">
              Change folder
            </button>
          </div>
        </div>
        <div class="index-footer">
          <div>
            <span
              class="status-dot"
              :class="{ amber: indexStatus.driving !== 'parked' }"
            ></span
            ><span>{{
              demo
                ? 'Sample library'
                : indexStatus.driving === 'parked'
                  ? indexStatus.running
                    ? `Indexing ${indexStatus.completed} / ${indexStatus.total}`
                    : 'Index up to date'
                  : `Index paused · ${indexStatus.driving === 'onroad' ? 'on the road' : 'driving state unknown'}`
            }}</span>
          </div>
          <button
            v-if="!demo"
            class="text-button"
            :disabled="indexStatus.driving !== 'parked'"
            @click="indexDrives(true)"
          >
            Rebuild index</button
          ><small v-if="indexStatus.errors"
            >{{ indexStatus.errors }} indexing errors · retry on refresh</small
          >
        </div>
        <div class="sidebar-footer">
          <ShieldCheck :size="15" /><span
            >Heavy processing runs only while parked</span
          >
        </div>
      </aside>

      <section
        class="detail"
        :class="{ 'session-view': view === 'drives' && !!active }"
      >
        <div v-if="demo" class="demo-banner">
          <span><span class="status-dot amber"></span> Sample recordings.</span
          ><button @click="openConnection">
            Connect your device <ArrowRight :size="14" />
          </button>
        </div>
        <div v-if="!online && !demo" class="offline-banner" role="status">
          You’re offline. The app is available, but recordings need a connection
          to your device.
        </div>
        <button
          v-if="view !== 'drives'"
          class="mobile-back text-button"
          @click="navigate('drives')"
        >
          <ArrowLeft :size="16" /> Recordings
        </button>
        <LiveView v-if="view === 'live' && !demo" />
        <div v-else-if="view === 'live'" class="no-selection">
          <Wifi :size="35" />
          <h1>Live cameras</h1>
          <p>
            Live view needs dash-backend and supported firmware. The sample
            library does not simulate a live connection.
          </p>
        </div>
        <section v-else-if="view === 'overview'" class="overview">
          <div class="eyebrow">INDEXED TELEMETRY</div>
          <h1>Recording overview</h1>
          <p>
            Measured from indexed qlogs. Totals cover {{ totals.covered }} of
            {{ totalSegments }} segments.
          </p>
          <div class="stat-grid">
            <div>
              <small>Recorded drives</small><strong>{{ drives.length }}</strong>
            </div>
            <div>
              <small>Measured distance</small
              ><strong>{{ totals.distance.toFixed(1) }} <span>km</span></strong>
            </div>
            <div>
              <small>Recorded time</small
              ><strong
                >{{ (totals.duration / 60).toFixed(1) }}
                <span>min</span></strong
              >
            </div>
          </div>
          <p v-if="!totals.covered" class="inline-error">
            Trip statistics will appear after parked indexing decodes qlogs. No
            mileage or duration is estimated from file counts.
          </p>
          <div class="activity-list">
            <h2>Drive activity</h2>
            <button
              v-for="trip in statistics.filter((item) => item.covered)"
              :key="trip.id"
              @click="
                selectDrive(drives.find((drive) => drive.id === trip.id)!)
              "
            >
              <span
                >{{ dateLabel(trip.date) }} · {{ timeLabel(trip.date) }}</span
              ><span
                >{{ trip.distance.toFixed(1) }} km ·
                {{ Math.round(trip.duration / 60) }} min</span
              ><span class="activity-track"
                ><span
                  :style="{
                    width: `${Math.max(2, (trip.distance / Math.max(1, ...statistics.map((entry) => entry.distance))) * 100)}%`,
                  }"
                ></span
              ></span>
            </button>
          </div>
          <div class="drive-note">
            <ShieldCheck :size="18" />
            <p>
              Distance uses valid GPS fixes, skips gaps over 10 seconds and
              implausible jumps. Missing or undecodable logs remain uncounted.
            </p>
          </div>
        </section>
        <template v-else-if="active">
          <div class="detail-heading">
            <div>
              <button
                class="mobile-back text-button"
                @click="mobileDetail = false"
              >
                <ArrowLeft :size="16" /> All drives
              </button>
              <div class="eyebrow">
                {{ demo ? 'SAMPLE DRIVE' : 'DRIVE REVIEW' }}
              </div>
              <h1>{{ dateLabel(active.date) }}</h1>
              <p>
                {{ timeLabel(active.date) }}
                <span v-if="!active.dated">· File timestamp</span>
              </p>
            </div>
            <SessionActions ref="actionMenu">
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('mp4');
                "
              >
                Download MP4
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('mcap');
                "
              >
                Download MCAP
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('raw');
                "
              >
                Download raw video
              </button>
              <button
                @click="
                  actionMenu?.close();
                  exportDialog?.open('zip');
                "
              >
                Download ZIP
              </button>
              <button
                @click="
                  actionMenu?.close();
                  originalFilesDialog?.showModal();
                "
              >
                Original files
              </button>
              <button
                @click="
                  saveDrive();
                  actionMenu?.close();
                "
              >
                {{ isSaved(active.id) ? 'Remove bookmark' : 'Bookmark drive' }}
              </button>
            </SessionActions>
          </div>
          <ExportDialog
            ref="exportDialog"
            :drive="active"
            :files="files"
            :sample="demo"
          />
          <Sheet
            ref="originalFilesDialog"
            title="Original files"
            close-label="Close original files"
            wide
            class="session-info-dialog"
          >
            <div class="modal-inner">
              <section class="files-section">
                <div class="section-heading">
                  <div>
                    <p>
                      Files from segment {{ (segment?.index ?? 0) + 1 }}.
                      Download directly to your device.
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
                          ? 'Illustrated sample · MP4'
                          : `${formatBytes(file.size)} · Original file`
                      }}</small></span
                    ><Download :size="17"
                  /></a>
                </div>
                <p
                  v-if="!files.length && !filesBusy && !fileError"
                  class="muted"
                >
                  No files in this segment. It may still be recording; refresh
                  to check again.
                </p>
              </section>
              <p class="muted">
                Bookmarks do not protect recordings from device retention.
                Download files to keep a copy.
              </p>
            </div>
          </Sheet>
          <div
            v-glass-tabs
            class="session-mobile-tabs segmented"
            aria-label="Session panels"
          >
            <button
              :class="{ selected: sessionPane === 'video' }"
              @click="sessionPane = 'video'"
            >
              Video
            </button>
            <button
              :class="{ selected: sessionPane === 'data' }"
              @click="sessionPane = 'data'"
            >
              Map &amp; data
            </button>
          </div>
          <ResizablePanels :class="{ 'show-data': sessionPane === 'data' }">
            <template #video>
              <div class="session-video">
                <div class="viewer-card">
                  <div class="viewer-toolbar">
                    <span
                      ><span class="status-dot"></span
                      >{{
                        demo ? 'Sample road camera' : 'Recording preview'
                      }}</span
                    ><label v-if="playable.length > 1" class="source-picker"
                      >Camera
                      <SelectMenu
                        v-model="selectedSource"
                        aria-label="Camera"
                        :options="
                          playable.map((file) => ({
                            value: demo
                              ? file.url
                              : recordingAddress(activeId, file.name),
                            label: file.name,
                          }))
                        " /></label
                    ><span v-else class="muted">{{
                      demo
                        ? 'Illustration'
                        : selectedSource
                          ? 'From your device'
                          : 'Original files'
                    }}</span>
                  </div>
                  <div
                    v-if="filesBusy"
                    class="player loading-player"
                    role="status"
                  >
                    <LoaderCircle class="spinning" :size="28" /><span
                      >Loading recording…</span
                    >
                  </div>
                  <div v-else-if="fileError" class="player loading-player">
                    <CircleHelp :size="30" />
                    <p role="alert">{{ fileError }}</p>
                    <button class="button" @click="loadFiles">Try again</button>
                  </div>
                  <RecordingPlayer
                    v-else
                    :key="active.id"
                    :source="selectedSource || undefined"
                    :sample="demo"
                  />
                </div>
              </div>
            </template>
            <template #data>
              <aside class="session-data">
                <section v-if="driveMetadata.length" class="trip-details">
                  <div class="section-heading">
                    <div>
                      <h2>Metadata</h2>
                      <p>
                        {{ driveMetadata.length }} /
                        {{ active.segments.length }} segments decoded
                      </p>
                    </div>
                  </div>
                  <div class="stat-grid">
                    <div>
                      <small>Measured distance</small
                      ><strong
                        >{{
                          driveMetadata
                            .reduce(
                              (sum, meta) => sum + (meta?.distanceKm || 0),
                              0,
                            )
                            .toFixed(1)
                        }}
                        <span>km</span></strong
                      >
                    </div>
                    <div>
                      <small>Recorded time</small
                      ><strong
                        >{{
                          (
                            driveMetadata.reduce(
                              (sum, meta) => sum + (meta?.duration || 0),
                              0,
                            ) / 60
                          ).toFixed(1)
                        }}
                        <span>min</span></strong
                      >
                    </div>
                    <div>
                      <small>Vehicle</small
                      ><strong class="vehicle-name">{{
                        driveMetadata.find((meta) => meta?.car)?.car ||
                        'Not recorded'
                      }}</strong>
                    </div>
                  </div>
                  <TripMap v-if="drivePoints.length" :points="drivePoints" />
                  <p v-else class="muted">
                    No valid GPS fixes in the indexed logs.
                  </p>
                </section>
                <template v-if="!driveMetadata.length"
                  ><h2>Metadata</h2>
                  <p>
                    Metadata is not available yet. Indexing runs while parked.
                  </p></template
                >
              </aside>
            </template>
          </ResizablePanels>
        </template>
        <div v-else class="no-selection">
          <HardDrive :size="42" />
          <h1>Select a drive</h1>
          <p>
            {{
              totalSegments
                ? 'Choose a drive from the library.'
                : 'Your device is connected. When recordings appear in this folder, refresh to see them here.'
            }}
          </p>
          <button class="button" :disabled="busy" @click="connect">
            Refresh recordings
          </button>
        </div>
        <footer v-if="view !== 'drives'" class="detail-footer">
          <span>LOCAL RECORDINGS</span
          ><span>dash <span class="footer-dot">·</span> Local access</span>
        </footer>
      </section>
    </main>

    <Sheet
      ref="connectionDialog"
      title="Device settings"
      close-label="Close device settings"
      @close="cancelConnection"
    >
      <div class="modal-inner">
        <div class="eyebrow">DASH BACKEND</div>

        <p>
          Dash is served by dash-backend on your device. Join its hotspot or
          local network and open the same address to reconnect.
        </p>
        <div class="backend-address">{{ appOrigin }}</div>
        <p>
          Background indexing and video processing run only when sunnypilot
          confirms the vehicle is parked. If driving state is unknown,
          processing stays paused.
        </p>
        <div v-if="connectionError" class="inline-error" role="alert">
          {{ connectionError }}
        </div>
        <button
          class="button primary full-width"
          :disabled="busy"
          @click="connect"
        >
          {{ busy ? 'Connecting…' : 'Reconnect to device' }}</button
        ><button
          v-if="!demo"
          class="text-button centered"
          :disabled="indexStatus.driving !== 'parked'"
          @click="
            indexDrives(true);
            connectionDialog?.close();
          "
        >
          Rebuild the recording index</button
        ><button v-if="demo" class="text-button centered" @click="disconnect">
          Leave sample library
        </button>
        <DiagnosticsDownload :available="!!backendOrigin" />
      </div>
    </Sheet>

    <Sheet ref="helpDialog" title="Help" close-label="Close help">
      <div class="modal-inner">
        <p>
          Dash reads recordings through dash-backend on your device. There are
          no accounts, uploads, or analytics.
        </p>
        <h3>Install Dash</h3>
        <p>
          On iPhone or iPad, open Dash in Safari, tap Share, then Add to Home
          Screen. On Android or desktop, use your browser’s Install app option.
          Installation requires HTTPS or localhost.
        </p>
        <h3>Offline access</h3>
        <p>
          The app shell works offline after your first visit. Watching or
          downloading recordings still needs a network connection to your
          device. Saved drives are bookmarks, not offline copies.
        </p>
        <h3>About recordings</h3>
        <p>
          Dash plays MP4, WebM, and supported H.264 transport-stream previews.
          Original HEVC cameras and logs are available to download. The Go
          backend decodes supported GPS and vehicle telemetry from qlogs while
          parked.
        </p>
        <button class="button primary full-width" @click="helpDialog?.close()">
          Got it <Check :size="17" />
        </button>
      </div>
    </Sheet>
    <Transition name="toast">
      <div v-if="notice" class="toast" role="status">
        <Check :size="17" />{{ notice }}
      </div>
    </Transition>
  </div>
</template>
