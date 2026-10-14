import { createApp } from 'vue';
import { registerSW } from 'virtual:pwa-register';
import App from './App.vue';
import { setupI18n } from './i18n';
import './style.css';
import './glass.css';
import './appearance.css';
import './shelf.css';
import './mobile.css';
import './viewport';
import './appearance';
// Keyboard navigation stays immediate; pointer interactions use surface motion.
document.addEventListener('keydown', (event) => {
  if (
    [
      'Tab',
      'Enter',
      'Escape',
      ' ',
      'ArrowLeft',
      'ArrowRight',
      'ArrowUp',
      'ArrowDown',
      'Home',
      'End',
    ].includes(event.key)
  ) {
    document.documentElement.dataset.interaction = 'keyboard';
  }
});
document.addEventListener('pointerdown', () => {
  document.documentElement.dataset.interaction = 'pointer';
});
registerSW({ immediate: true });
async function start() {
  const app = createApp(App);
  await setupI18n(app);
  app.mount('#app');
}
void start();
