# Maps

Dash uses MapLibre GL JS 6 with OpenStreetMap's Shortbread vector tile service. No Mapbox account, access token, or other map credential is used. The browser renders the basemap, route, and playback marker. dash-backend serves the app and recorded metadata; it does not generate or proxy map tiles.

Dash's own light/dark style is bundled with the frontend and uses local system fonts. It does not request OSM's demonstration styles/font assets or Mapbox styles. [MapLibre local-font documentation](https://maplibre.org/maplibre-gl-js/docs/examples/style-labels-with-local-fonts/) describes the renderer behavior. OSM supports the Shortbread v1 TileJSON endpoint; vector data tops out at zoom 14 and the browser overzooms it for closer views.

## Service configuration

The frontend fetches `/map-config.json` when a visible map initializes. The shipped file is:

```json
{
  "tileJSON": "https://vector.openstreetmap.org/shortbread_v1/tilejson.json"
}
```

Edit `public/map-config.json` for the next build, or the installed app directory's `map-config.json` to change the source without rebuilding. Reload the page after changing it. On a device, the default path is `/data/dash/dist/map-config.json`. Installation replaces `dist`, so retain your operator configuration when updating. An alternative source must implement the Shortbread schema and permit your usage. Addresses may be HTTPS or same-origin paths; an HTTPS app rejects HTTP sources. No configuration is exposed as an end-user setting.

The runtime config is not precached by the PWA. Missing or invalid config produces a GPS-only map with a background-unavailable notice, rather than contacting an unexpected provider. Tiles load directly from the configured source, with the browser's normal Referer and HTTP caching. Keep a valid Referer policy when placing Dash behind a reverse proxy.

## OSM usage and limitations

Follow the [OSM vector tile usage policy](https://operations.osmfoundation.org/policies/vector/) and [attribution requirements](https://www.openstreetmap.org/copyright). Dash displays expanded attribution and a map-issue link. It requests only the visible viewport during interactive viewing, does not bulk download or prefetch regions, and does not bypass browser tile caching. Tile URLs should remain changeable because OSM's donation-funded service is best-effort, has no SLA, and may restrict disruptive usage. Production deployments with substantial traffic should arrange an appropriate Shortbread provider.

The PWA caches application assets, not map tiles. A LAN connection to the device does not itself provide internet access for street backgrounds. Recorded GPS and video remain usable while the tile service is unavailable. Without WebGL, Dash presents a static GPS route preview; interactive map controls are disabled. No background tile-generation job or extra compute on the comma device is introduced.

## Verification

`make map-smoke` verifies vector rendering, visible attribution, theme/camera preservation, unavailable tiles, unsupported WebGL, deferred phone map initialization, and absence of Mapbox requests. UI smoke suites use an entirely synthetic Shortbread tile fixture to avoid repeated sweeps against community servers. A one-viewport live check is performed separately when changing the provider. Physical iPhone/Safari behavior remains a separate hardware check.
