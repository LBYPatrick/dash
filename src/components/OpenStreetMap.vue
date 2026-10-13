<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue';
import {
  map as createMap,
  tileLayer,
  polyline,
  latLngBounds,
  control,
  type Map,
  type Polyline,
} from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { resolvedTheme } from '../appearance';
import type { Position } from '../indexer';
const props = defineProps<{ points: Position[] }>();
const container = ref<HTMLDivElement>();
const unavailable = ref(false);
let map: Map | undefined;
let outline: Polyline;
let route: Polyline;
let observer: ResizeObserver;
let fitted = false;
const coordinates = () =>
  props.points.map((p) => [p.latitude, p.longitude] as [number, number]);
function fitRoute() {
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
  if (fit) {
    fitted = false;
    fitRoute();
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
  observer = new ResizeObserver(() => {
    map?.invalidateSize({ animate: false, pan: false });
    if (!fitted) fitRoute();
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
    ></div>
    <div class="map-toolbar" role="group" aria-label="Map controls">
      <button type="button" aria-label="Zoom in" @click="map?.zoomIn()">
        +
      </button>
      <button type="button" aria-label="Zoom out" @click="map?.zoomOut()">
        −
      </button>
      <span class="map-toolbar-divider"></span>
      <button type="button" :disabled="!points.length" @click="fitRoute">
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
