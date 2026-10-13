import { createApp } from 'vue';
import { registerSW } from 'virtual:pwa-register';
import App from './App.vue';
import './style.css';
import './glass.css';
import './appearance.css';
import './appearance';
// Keyboard navigation stays immediate; pointer interactions use surface motion.
document.addEventListener('keydown', () => {
  document.documentElement.dataset.interaction = 'keyboard';
});
document.addEventListener('pointerdown', () => {
  document.documentElement.dataset.interaction = 'pointer';
});
registerSW({ immediate: true });
createApp(App).mount('#app');
