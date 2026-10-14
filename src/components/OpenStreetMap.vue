<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue';
import {
  map as createMap,
  tileLayer,
  polyline,
  latLngBounds,
  control,
  circleMarker,
  type CircleMarker,
  type Map,
  type Polyline,
} from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { resolvedTheme } from '../appearance';
import type { Position } from '../indexer';
const props = defineProps<{ points: Position[]; position?: Position }>();
const following = defineModel<boolean>('following', { required: true });
let marker: CircleMarker | undefined;
function updatePosition() {
  // Hidden mobile panels catch up on resize instead of panning offscreen.
  if (
    document.hidden ||
    !container.value?.clientWidth ||
    !container.value.clientHeight
  )
    return;
  if (!map || !props.position) {
    if (marker) {
      marker.remove();
      marker = undefined;
    }
    return;
  }
  const point: [number, number] = [
    props.position.latitude,
    props.position.longitude,
  ];
  if (!marker)
    marker = circleMarker(point, {
      radius: 6,
      color: '#fff',
      weight: 2,
      fillColor: '#006ac6',
      fillOpacity: 1,
      interactive: false,
    }).addTo(map);
  else marker.setLatLng(point);
  if (following.value) {
    if (map.getZoom() < 16) map.setView(point, 16, { animate: false });
    else map.panTo(point, { animate: false });
  }
}
function follow() {
  following.value = true;
  updatePosition();
}
watch(() => props.position, updatePosition);
watch(following, (value) => {
  if (value) updatePosition();
});
const container = ref<HTMLDivElement>();
const unavailable = ref(false);
let map: Map | undefined;
let outline: Polyline;
let route: Polyline;
let observer: ResizeObserver;
let fitted = false;
const coordinates = () =>
  props.points.map((p) => [p.latitude, p.longitude] as [number, number]);
function fitRoute(user = false) {
  if (user) following.value = false;
  if (
    !map ||
    !props.points.length ||
    !container.value?.clientWidth ||
    !container.value.clientHeight
  )
    return;
  fitted = true;
  map.fitBounds(latLngBounds(coordinates()), {
    paddingTopLeft: [25, 74],
    paddingBottomRight: [25, 30],
    maxZoom: 19,
    animate: false,
  });
}
function draw(fit = true) {
  if (!map) return;
  const points = coordinates();
  outline
    .setLatLngs(points)
    .setStyle({ color: resolvedTheme.value === 'dark' ? '#192126' : '#fff' });
  route.setLatLngs(points).setStyle({
    color: resolvedTheme.value === 'dark' ? '#a4e8cd' : '#286b50',
  });
  // Index summaries refresh independently of playback. Updating the route
  // must never reset the camera or fight a user's current view.
  if (fit && !fitted) {
    fitRoute();
    updatePosition();
  }
}
onMounted(() => {
  map = createMap(container.value!, {
    zoomControl: false,
    attributionControl: false,
    scrollWheelZoom: false,
  }).setView(props.points.length ? coordinates()[0] : [0, 0], 12);
  const tiles = tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    attribution:
      '© <a href="https://www.openstreetmap.org/copyright">OpenStreetMap contributors</a>',
  });
  tiles.on('tileerror', () => {
    unavailable.value = true;
  });
  tiles.on('tileload', () => {
    unavailable.value = false;
  });
  tiles.addTo(map);
  control.attribution({ position: 'bottomright', prefix: false }).addTo(map);
  outline = polyline([], { weight: 7, opacity: 1, interactive: false }).addTo(
    map,
  );
  route = polyline([], { weight: 3, opacity: 1, interactive: false }).addTo(
    map,
  );
  draw();
  updatePosition();
  map.on('dragstart', () => (following.value = false));
  observer = new ResizeObserver(() => {
    map?.invalidateSize({ animate: false, pan: false });
    if (!fitted) {
      fitRoute();
    }
    updatePosition();
  });
  observer.observe(container.value!);
});
watch(
  () => props.points,
  () => draw(),
);
watch(resolvedTheme, () => draw(false));
onBeforeUnmount(() => {
  observer?.disconnect();
  map?.remove();
  map = undefined;
});
</script>
<template>
  <section
    class="trip-map osm-map"
    :class="{ 'osm-map--dark': resolvedTheme === 'dark' }"
  >
    <div
      ref="container"
      class="map-canvas"
      role="region"
      aria-label="Recorded GPS route"
      @pointerdown="following = false"
      @wheel="following = false"
      @keydown="following = false"
    ></div>
    <div class="map-toolbar" role="group" aria-label="Map controls">
      <button
        type="button"
        aria-label="Zoom in"
        @click="
          following = false;
          map?.zoomIn();
        "
      >
        +
      </button>
      <button
        type="button"
        aria-label="Zoom out"
        @click="
          following = false;
          map?.zoomOut();
        "
      >
        −
      </button>
      <button
        type="button"
        :aria-pressed="following"
        :disabled="!position"
        @click="follow"
      >
        Follow
      </button>
      <span class="map-toolbar-divider"></span>
      <button type="button" :disabled="!points.length" @click="fitRoute(true)">
        Full route
      </button>
    </div>
    <p v-if="unavailable" class="map-notice" role="status">
      Map unavailable. Check your connection.
    </p>
  </section>
</template>
<style scoped>
.osm-map :deep(.leaflet-tile-pane) {
  filter: saturate(0.75);
}
.osm-map--dark :deep(.leaflet-tile-pane) {
  filter: invert(1) hue-rotate(180deg) saturate(0.3) brightness(0.85);
}
.osm-map :deep(.leaflet-container) {
  background: var(--color-base-300);
  font-family: var(--font-ui);
}
.osm-map :deep(.leaflet-control-attribution) {
  background: var(--color-base-100);
  color: var(--ink);
  font-size: 11px;
}
.osm-map :deep(.leaflet-control-attribution a) {
  color: var(--color-primary-text);
}
/* Leaflet's internal panes should not cover Dash's shared map controls. */
.osm-map :deep(.leaflet-pane) {
  z-index: 1;
}
.osm-map :deep(.leaflet-control-container) {
  position: relative;
  z-index: 2;
}
.osm-map .map-toolbar {
  z-index: 3;
}
</style>
