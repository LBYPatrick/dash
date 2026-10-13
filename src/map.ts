// Connect's published browser token; override for your own deployment.
export const mapboxToken =
  import.meta.env.VITE_MAPBOX_TOKEN ||
  '';
export const mapboxStyle =
  import.meta.env.VITE_MAPBOX_STYLE || 'mapbox://styles/mapbox/light-v11';
