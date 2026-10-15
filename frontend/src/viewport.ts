// Chromium resizes through interactive-widget; Safari exposes the keyboard's
// usable viewport separately. Only follow unzoomed keyboard resizes so pinch
// zoom remains native and never shrinks the app into the zoomed viewport.
const viewport = window.visualViewport;
function update() {
  const editing = document.activeElement?.matches(
    'input:not([type=range]),textarea',
  );
  if (
    viewport &&
    viewport.scale === 1 &&
    editing &&
    viewport.height < innerHeight - 100
  ) {
    document.documentElement.style.setProperty(
      '--app-height',
      `${viewport.height + viewport.offsetTop}px`,
    );
  } else document.documentElement.style.removeProperty('--app-height');
}
viewport?.addEventListener('resize', update);
viewport?.addEventListener('scroll', update);
document.addEventListener('focusin', update);
document.addEventListener('focusout', () => requestAnimationFrame(update));
window.addEventListener('resize', update);
