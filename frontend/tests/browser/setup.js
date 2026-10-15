import { beforeEach, afterEach } from 'vite-plus/test';

// Each acceptance test owns its simulator state. Fully mocked UI tests may use
// a static preview; never try to change state on a real firmware backend.
const origin = process.env.DASH_TEST_URL || 'http://127.0.0.1:8088';
let simulated = false;
async function setState(state) {
  const response = await fetch(`${origin}/api/simulation/state`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state }),
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok)
    throw new Error(`Simulator state returned HTTP ${response.status}`);
}
beforeEach(async () => {
  simulated = false;
  try {
    const response = await fetch(`${origin}/api/info`, {
      signal: AbortSignal.timeout(3000),
    });
    simulated = (await response.json()).simulation === true;
  } catch {
    /* Static previews can be covered with browser-only API fixtures. */
  }
  if (simulated) await setState('parked');
});
afterEach(async () => {
  if (simulated) await setState('unknown');
});
