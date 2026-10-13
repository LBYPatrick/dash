<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue';
import {
  Map,
  AttributionControl,
  LngLatBounds,
  type GeoJSONSource,
} from 'mapbox-gl';
import { mapboxToken, mapStyleForTheme } from '../map';
import { resolvedTheme } from '../appearance';
import type { Position } from '../indexer';
const props = defineProps<{ points: Position[] }>();
const container = ref<HTMLDivElement>();
const unavailable = ref(false);
let map: Map | undefined;
let observer: ResizeObserver;
let fallback = false;
let styleGeneration = 0;
function fitRoute() {
  if (!map || !props.points.length) return;
  const bounds = new LngLatBounds();
  for (const point of props.points)
    bounds.extend([point.longitude, point.latitude]);
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
  if (fit) fitRoute();
}
onMounted(() => {
  map = new Map({
    container: container.value!,
    style: mapStyleForTheme(resolvedTheme.value),
    accessToken: mapboxToken,
    center: props.points.length
      ? [props.points[0].longitude, props.points[0].latitude]
      : [0, 0],
    zoom: 12,
    maxPitch: 0,
    dragRotate: false,
    attributionControl: false,
  });
  map.touchZoomRotate.disableRotation();
  map.scrollZoom.disable();
  map.addControl(new AttributionControl({ compact: true }), 'bottom-right');
  map.on('error', () => {
    const current = map;
    const generation = styleGeneration;
    if (!current) return;
    unavailable.value = true;
    // Changing styles synchronously inside a Mapbox error callback can
    // invalidate the style while its own update is still running.
    queueMicrotask(() => {
      if (
        map !== current ||
        generation !== styleGeneration ||
        fallback ||
        current.getSource('route')
      )
        return;
      fallback = true;
      current.setStyle({
        version: 8,
        sources: {},
        layers: [
          {
            id: 'background',
            type: 'background',
            paint: {
              'background-color':
                resolvedTheme.value === 'dark' ? '#192126' : '#f3f4f2',
            },
          },
        ],
      });
    });
  });
  map.on('style.load', () => {
    if (!map) return;
    unavailable.value = fallback;
    // Keep route-map labels readable against either basemap.
    for (const layer of map!.getStyle()?.layers || []) {
      if (layer.type !== 'symbol' || !layer.layout?.['text-field']) continue;
      map!.setPaintProperty(
        layer.id,
        'text-color',
        resolvedTheme.value === 'dark' ? '#edf3f7' : '#3d494f',
      );
      map!.setPaintProperty(
        layer.id,
        'text-halo-color',
        resolvedTheme.value === 'dark' ? '#192126' : '#fff',
      );
      map!.setPaintProperty(layer.id, 'text-halo-width', 1);
    }
    map!.addSource('route', {
      type: 'geojson',
      data: { type: 'FeatureCollection', features: [] },
    });
    const layout = { 'line-join': 'round', 'line-cap': 'round' } as const;
    map!.addLayer({
      id: 'routeOutline',
      type: 'line',
      source: 'route',
      layout,
      paint: {
        'line-color': resolvedTheme.value === 'dark' ? '#192126' : '#fff',
        'line-width': 7,
      },
    });
    map!.addLayer({
      id: 'routeLine',
      type: 'line',
      source: 'route',
      layout,
      paint: {
        'line-color': resolvedTheme.value === 'dark' ? '#a4e8cd' : '#286b50',
        'line-width': 3,
      },
    });
    draw(false);
  });
  observer = new ResizeObserver(() => {
    map?.resize();
  });
  observer.observe(container.value!);
  fitRoute();
});
watch(
  () => props.points,
  () => draw(),
);
watch(resolvedTheme, (theme) => {
  styleGeneration++;
  fallback = false;
  unavailable.value = false;
  // Rebuild the style so style.load reinstalls our route layers as well.
  // Diffing a raster style can remove custom layers without that event.
  map?.setStyle(mapStyleForTheme(theme), {
    diff: false,
    localFontFamily: undefined,
    localIdeographFontFamily: undefined,
  });
});
onBeforeUnmount(() => {
  observer?.disconnect();
  const previous = map;
  map = undefined;
  styleGeneration++;
  previous?.remove();
});
</script>
<template>
  <section class="trip-map">
    <div
      ref="container"
      class="map-canvas"
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
