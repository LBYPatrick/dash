export type WorkspaceView = 'drives' | 'overview' | 'live';
export interface WorkspaceRoute {
  view: WorkspaceView;
  driveId?: string;
  returnView?: 'drives' | 'overview';
}
const key = 'dashWorkspace';
export function workspaceRoute(value: unknown): WorkspaceRoute | undefined {
  if (!value || typeof value !== 'object') return;
  const route = value as Record<string, unknown>;
  if (
    typeof route.view !== 'string' ||
    !['drives', 'overview', 'live'].includes(route.view)
  )
    return;
  if (
    route.driveId !== undefined &&
    (route.view !== 'drives' ||
      typeof route.driveId !== 'string' ||
      !route.driveId ||
      route.driveId.length > 512)
  )
    return;
  return {
    view: route.view as WorkspaceView,
    ...(route.driveId
      ? {
          driveId: route.driveId as string,
          returnView: route.returnView === 'overview' ? 'overview' : 'drives',
        }
      : {}),
  };
}
// Same-document entries give browser/PWA Back the same meaning as the visible
// return button. The URL and native browser edge-swipe stay under browser control.
export class WorkspaceHistory {
  current: WorkspaceRoute;
  private depth: number;
  private pop = (event: PopStateEvent) => {
    const entry = event.state?.[key];
    this.current = workspaceRoute(entry?.route) || { view: 'drives' };
    this.depth =
      Number.isSafeInteger(entry?.depth) && entry.depth >= 0 ? entry.depth : 0;
    this.apply(this.current);
  };
  constructor(private apply: (route: WorkspaceRoute) => void) {
    const entry = history.state?.[key];
    this.current = workspaceRoute(entry?.route) || { view: 'drives' };
    this.depth =
      Number.isSafeInteger(entry?.depth) && entry.depth >= 0 ? entry.depth : 0;
    this.write(true);
    window.addEventListener('popstate', this.pop);
  }
  private write(replace: boolean) {
    const state = {
      ...history.state,
      [key]: { route: this.current, depth: this.depth },
    };
    if (replace) history.replaceState(state, '');
    else history.pushState(state, '');
  }
  navigate(route: WorkspaceRoute, replace = false) {
    if (JSON.stringify(this.current) === JSON.stringify(route)) {
      this.apply(route);
      return;
    }
    this.current = route;
    if (!replace) this.depth++;
    this.write(replace);
    this.apply(route);
  }
  back(fallback: WorkspaceRoute) {
    if (this.depth > 0) history.back();
    else this.navigate(fallback, true);
  }
  dispose() {
    window.removeEventListener('popstate', this.pop);
  }
}
