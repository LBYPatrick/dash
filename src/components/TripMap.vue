<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue';
import {
  Map,
  AttributionControl,
  LngLatBounds,
  type GeoJSONSource,
} from 'mapbox-gl';
import { mapboxToken, mapboxStyle } from '../map';
import type { Position } from '../indexer';
const props = defineProps<{ points: Position[] }>();
const container = ref<HTMLDivElement>();
const unavailable = ref(false);
let map: Map | undefined;
let observer: ResizeObserver;
let fallback = false;
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
function draw() {
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
  fitRoute();
}
onMounted(() => {
  map = new Map({
    container: container.value!,
    style: mapboxStyle,
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
    unavailable.value = true;
    if (!fallback && !map?.getSource('route')) {
      fallback = true;
      map?.setStyle({
        version: 8,
        sources: {},
        layers: [
          {
            id: 'background',
            type: 'background',
            paint: { 'background-color': '#f3f4f2' },
          },
        ],
      });
    }
  });
  map.on('style.load', () => {
    unavailable.value = fallback;
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
      paint: { 'line-color': '#fff', 'line-width': 7 },
    });
    map!.addLayer({
      id: 'routeLine',
      type: 'line',
      source: 'route',
      layout,
      paint: { 'line-color': '#286b50', 'line-width': 3 },
    });
    draw();
  });
  observer = new ResizeObserver(() => {
    map?.resize();
  });
  observer.observe(container.value!);
  fitRoute();
});
watch(() => props.points, draw);
onBeforeUnmount(() => {
  observer?.disconnect();
  map?.remove();
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
