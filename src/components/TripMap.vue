<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue';
import L from 'leaflet';
import type { Position } from '../indexer';
const props = defineProps<{ points: Position[] }>();
const container = ref<HTMLDivElement>();
let map: L.Map;
let line: L.Polyline;
let observer: ResizeObserver;
function draw() {
  if (!map || !props.points.length) return;
  const coords: L.LatLngTuple[] = props.points.map((p) => [
    p.latitude,
    p.longitude,
  ]);
  line?.remove();
  line = L.polyline(coords, { color: '#377650', weight: 4 }).addTo(map);
  map.fitBounds(line.getBounds(), { padding: [25, 25], maxZoom: 19 });
}
onMounted(() => {
  map = L.map(container.value!, { scrollWheelZoom: false }).setView([0, 0], 2);
  draw();
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    attribution:
      '© <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
  }).addTo(map);
  observer = new ResizeObserver(() => {
    map.invalidateSize();
    draw();
  });
  observer.observe(container.value!);
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
    <p>Recorded GPS positions.</p>
  </section>
</template>
