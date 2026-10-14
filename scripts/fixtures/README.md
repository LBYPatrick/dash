# Synthetic map fixture

`shortbread.mvt` contains invented streets, a park, water, one building and a place label in tile coordinates. It is generated using `@maplibre/vt-pbf` and contains no OpenStreetMap data or device positions. UI smoke tests intercept OSM tile requests with this file so repeated viewport/theme tests do not contact community infrastructure.
