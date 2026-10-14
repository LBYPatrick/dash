<script setup lang="ts">
import { useTranslation } from 'i18next-vue';
import { locale } from '../i18n';
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue';
import {
  Map,
  AttributionControl,
  Marker,
  LngLatBounds,
  type GeoJSONSource,
  setWorkerUrl,
  setWorkerCount,
} from 'maplibre-gl';
import workerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url';
import 'maplibre-gl/dist/maplibre-gl.css';
import {
  mapStyleForTheme,
  parseMapConfig,
  osmAttribution,
  type MapConfig,
} from '../map';
import { resolvedTheme } from '../appearance';
import type { Position } from '../indexer';
const { t } = useTranslation();
const props = defineProps<{ points: Position[]; position?: Position }>();
const following = ref(true);
setWorkerUrl(workerUrl);
setWorkerCount(1);
let marker: Marker | undefined;
function updatePosition() {
  // Hidden mobile panels catch up on resize instead of panning offscreen.
  if (
    document.hidden ||
    !container.value?.clientWidth ||
    !container.value.clientHeight
  )
    return;
  if (!map || !props.position) {
    marker?.remove();
    marker = undefined;
    return;
  }
  const point: [number, number] = [
    props.position.longitude,
    props.position.latitude,
  ];
  if (!marker) {
    const element = document.createElement('div');
    element.className = 'playback-map-marker';
    element.setAttribute('aria-hidden', 'true');
    marker = new Marker({ element, subpixelPositioning: true })
      .setLngLat(point)
      .addTo(map);
  } else marker.setLngLat(point);
  if (following.value)
    map.jumpTo({
      center: point,
      zoom: Math.max(map.getZoom(), 16),
      // Full-route fitting uses asymmetric padding; do not retain that camera
      // offset when returning to the playback position.
      padding: { top: 0, bottom: 0, left: 0, right: 0 },
    });
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
let observer: ResizeObserver;
let config: MapConfig | undefined;
let initializing = false;
const request = new AbortController();
const rendererFailed = ref(false);
const ready = ref(false);
let fitted = false;
function fitRoute(user = false) {
  if (user) following.value = false;
  if (
    !map ||
    !props.points.length ||
    !container.value?.clientWidth ||
    !container.value.clientHeight
  )
    return;
  const bounds = new LngLatBounds();
  for (const point of props.points)
    bounds.extend([point.longitude, point.latitude]);
  fitted = true;
  map.fitBounds(bounds, {
    padding: { top: 74, bottom: 30, left: 25, right: 25 },
    maxZoom: 19,
    duration: 0,
  });
}
function draw(fit = true) {
  const route = map?.getSource('route') as GeoJSONSource | undefined;
  if (!route) return;
  route.setData({
    type: 'FeatureCollection',
    features:
      props.points.length > 1
        ? [
            {
              type: 'Feature',
              properties: {},
              geometry: {
                type: 'LineString',
                coordinates: props.points.map((p) => [p.longitude, p.latitude]),
              },
            },
          ]
        : [],
  });
  // Index summaries refresh independently of playback. Updating the route
  // must never reset the camera or fight a user's current view.
  if (fit && !fitted) {
    fitRoute();
    updatePosition();
  }
}
// Only initialize a map when its panel is visible. Hidden mobile panels do
// not fetch tiles or allocate a WebGL context before the user opens them.
async function initialize() {
  if (
    map ||
    initializing ||
    rendererFailed.value ||
    !container.value?.clientWidth ||
    !container.value.clientHeight
  )
    return;
  initializing = true;
  try {
    const response = await fetch('/map-config.json', {
      signal: AbortSignal.any([request.signal, AbortSignal.timeout(2500)]),
      cache: 'no-store',
    });
    if (!response.ok) throw new Error('Map configuration unavailable');
    config = parseMapConfig(await response.json(), location.origin);
  } catch {
    if (request.signal.aborted) return;
    unavailable.value = true;
  }
  if (request.signal.aborted) return;
  if (!container.value?.clientWidth || !container.value.clientHeight) {
    initializing = false;
    return;
  }
  try {
    const position = props.position || props.points[0];
    map = new Map({
      container: container.value!,
      style: mapStyleForTheme(resolvedTheme.value, config),
      center: position ? [position.longitude, position.latitude] : [0, 0],
      zoom: props.position ? 16 : 12,
      maxZoom: 19,
      maxPitch: 0,
      dragRotate: false,
      pitchWithRotate: false,
      attributionControl: false,
      canvasContextAttributes: { antialias: false },
      maxTileCacheSize: 64,
      maxTileCacheZoomLevels: 2,
      pixelRatio: Math.min(devicePixelRatio, 2),
      fadeDuration: 0,
    });
    map.getCanvas().setAttribute('aria-label', t('map.recordedGpsRoute'));
    ready.value = true;
    map.on('webglcontextlost', () => {
      rendererFailed.value = true;
      ready.value = false;
    });
    map.on('webglcontextrestored', () => {
      rendererFailed.value = false;
      ready.value = true;
    });
    map.on('dragstart', () => (following.value = false));
    map.touchZoomRotate.disableRotation();
    map.scrollZoom.disable();
    // Always show attribution, including before tiles arrive or on failure.
    map.addControl(
      new AttributionControl({
        compact: false,
        customAttribution: config ? undefined : osmAttribution,
      }),
      'bottom-right',
    );
    map.on('error', () => {
      unavailable.value = true;
    });
    map.on('sourcedata', (event) => {
      if (
        event.sourceId === 'osm' &&
        event.sourceDataType === 'content' &&
        event.tile
      )
        unavailable.value = false;
    });
    map.on('style.load', () => {
      draw(false);
      if (following.value && props.position) fitted = true;
      else if (!fitted) fitRoute();
      updatePosition();
    });
  } catch {
    map?.remove();
    map = undefined;
    rendererFailed.value = true;
    ready.value = false;
  }
}
function resize() {
  if (!container.value?.clientWidth || !container.value.clientHeight) return;
  if (!map) {
    void initialize();
    return;
  }
  map.resize();
  if (!fitted) fitRoute();
  updatePosition();
}
onMounted(() => {
  observer = new ResizeObserver(resize);
  observer.observe(container.value!);
  void initialize();
});
watch(
  () => props.points,
  () => draw(),
);
watch(locale, () =>
  map?.getCanvas().setAttribute('aria-label', t('map.recordedGpsRoute')),
);
watch(resolvedTheme, (theme) => {
  if (!map) return;
  // Paint changes preserve sources, the camera and downloaded tiles.
  for (const layer of mapStyleForTheme(theme, config).layers) {
    if (!map.getLayer(layer.id) || !layer.paint) continue;
    for (const [property, value] of Object.entries(layer.paint))
      map.setPaintProperty(
        layer.id,
        property as Parameters<Map['setPaintProperty']>[1],
        value,
      );
  }
});
// An untextured GPS preview still works when WebGL itself is unavailable.
const preview = computed(() => {
  const points = props.points;
  if (!points.length) return { path: '', project: (_: Position) => [500, 300] };
  const longitudeScale = Math.max(
    0.01,
    Math.cos((points[0].latitude * Math.PI) / 180),
  );
  let minX = Infinity,
    maxX = -Infinity,
    minY = Infinity,
    maxY = -Infinity;
  for (const point of points) {
    minX = Math.min(minX, point.longitude * longitudeScale);
    maxX = Math.max(maxX, point.longitude * longitudeScale);
    minY = Math.min(minY, point.latitude);
    maxY = Math.max(maxY, point.latitude);
  }
  const scale = Math.min(
    900 / Math.max(maxX - minX, 0.00001),
    450 / Math.max(maxY - minY, 0.00001),
  );
  const project = (point: Position) => [
    500 + (point.longitude * longitudeScale - (maxX + minX) / 2) * scale,
    320 - (point.latitude - (maxY + minY) / 2) * scale,
  ];
  return {
    path: points.map((point) => project(point).join(',')).join(' '),
    project,
  };
});
onBeforeUnmount(() => {
  request.abort();
  observer?.disconnect();
  marker?.remove();
  marker = undefined;
  map?.remove();
  map = undefined;
});
</script>
<template>
  <section class="trip-map">
    <div
      ref="container"
      class="map-canvas"
      role="region"
      :aria-label="t('map.recordedGpsRoute')"
      @pointerdown="following = false"
      @wheel="following = false"
      @keydown="following = false"
    ></div>
    <svg
      v-if="rendererFailed"
      class="map-route-fallback"
      viewBox="0 0 1000 600"
      role="img"
      :aria-label="t('map.recordedRouteWithoutMapBackground')"
    >
      <polyline
        :points="preview.path"
        fill="none"
        stroke="currentColor"
        stroke-width="3"
        vector-effect="non-scaling-stroke"
      />
      <circle
        v-if="position"
        :cx="preview.project(position)[0]"
        :cy="preview.project(position)[1]"
        r="8"
        fill="currentColor"
      />
    </svg>
    <div class="map-toolbar" role="group" :aria-label="t('map.mapControls')">
      <button
        type="button"
        :aria-label="t('map.zoomIn')"
        :disabled="!ready"
        @click="
          following = false;
          map?.zoomIn();
        "
      >
        +
      </button>
      <button
        type="button"
        :aria-label="t('map.zoomOut')"
        :disabled="!ready"
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
        :disabled="!ready || !position"
        @click="follow"
      >
        {{ t('map.follow') }}
      </button>
      <span class="map-toolbar-divider"></span>
      <button
        type="button"
        :disabled="!ready || !points.length"
        @click="fitRoute(true)"
      >
        {{ t('map.fullRoute') }}
      </button>
    </div>
    <p v-if="unavailable || rendererFailed" class="map-notice" role="status">
      {{
        rendererFailed
          ? t('map.interactiveMapUnavailableShowingTheRecordedRoute')
          : t('map.mapBackgroundUnavailableRecordedGpsStillWorks')
      }}
    </p>
  </section>
</template>

<style scoped>
.map-route-fallback {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  color: var(--green);
  background: var(--surface-subtle);
}
</style>
