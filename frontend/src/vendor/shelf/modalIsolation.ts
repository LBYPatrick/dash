// Browser adaptation: Shelf's Electron focus trap also needs accessibility-tree
// isolation here. Restore authored inert values when the last modal closes.
const stack: HTMLElement[] = [];
const previous = new Map<HTMLElement, boolean>();

function update() {
  for (const [element, inert] of previous) element.inert = inert;
  previous.clear();
  let branch: HTMLElement | undefined = stack.at(-1);
  while (branch && branch !== document.body) {
    const parent = branch.parentElement;
    if (!parent) break;
    for (const sibling of parent.children) {
      if (sibling === branch || !(sibling instanceof HTMLElement)) continue;
      previous.set(sibling, sibling.inert);
      sibling.inert = true;
    }
    branch = parent;
  }
}

export function isolateModal(panel: HTMLElement) {
  stack.push(panel);
  update();
  return () => {
    const index = stack.indexOf(panel);
    if (index >= 0) stack.splice(index, 1);
    update();
  };
}
