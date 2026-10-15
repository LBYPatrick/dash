import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { VitePWA } from 'vite-plugin-pwa';

export default defineConfig({
  // Shared, ignored local configuration remains at the repository root.
  envDir: '..',
  server: {
    proxy: {
      '/api': {
        target: process.env.DASH_BACKEND_URL || 'http://127.0.0.1:8088',
        changeOrigin: true,
        rewrite: (path) => path,
        configure: (proxy) =>
          proxy.on('proxyReq', (req) => req.removeHeader('origin')),
      },
      '/files': {
        target: process.env.DASH_BACKEND_URL || 'http://127.0.0.1:8088',
        changeOrigin: true,
        configure: (proxy) =>
          proxy.on('proxyReq', (req) => req.removeHeader('origin')),
      },
    },
  },
  plugins: [
    vue(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['icon.svg', 'icon-192.png', 'icon-512.png'],
      manifest: {
        name: 'Dash — Your drives',
        short_name: 'Dash',
        description: 'A direct connection to your drive recordings.',
        theme_color: '#eef2f6',
        background_color: '#eef2f6',
        display: 'standalone',
        start_url: '/',
        icons: [
          { src: '/icon-192.png', sizes: '192x192', type: 'image/png' },
          {
            src: '/icon-512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'any maskable',
          },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
        navigateFallbackDenylist: [/^\/api\//, /^\/files\//],
      },
    }),
  ],
});
