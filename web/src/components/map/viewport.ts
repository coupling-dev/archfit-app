// The diagram's view is one number and one corner: k screen pixels per world unit, and the world point at the top-left
// of the viewport. The SVG viewBox is derived from it, so there is no transform to keep in step.
export interface View {
  k: number;
  x: number;
  y: number;
}
export interface Size {
  w: number;
  h: number;
}

// Module names are 14 px in world units; a name is never drawn smaller than 11 px when the diagram opens or fits.
export const LABEL_PX = 14;
export const MIN_LABEL_PX = 11;
export const FLOOR = MIN_LABEL_PX / LABEL_PX;
export const MIN_ZOOM = 0.1;
export const MAX_ZOOM = 4;
// Fit to width may enlarge a small chart, but not past this: a 6-module graph would otherwise fill the box with 20 px names.
export const FIT_MAX = 1.5;
// A quarter of the viewport, or of the chart where it is smaller, always stays on screen, so a drag or a key cannot
// lose the chart even at the highest zoom.
const KEEP = 0.25;

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

export function clampView(view: View, world: Size, vp: Size): View {
  const k = clamp(view.k, MIN_ZOOM, MAX_ZOOM);
  const seen = { w: vp.w / k, h: vp.h / k };
  const keepW = Math.min(seen.w, world.w) * KEEP;
  const keepH = Math.min(seen.h, world.h) * KEEP;
  return {
    k,
    x: clamp(view.x, keepW - seen.w, world.w - keepW),
    y: clamp(view.y, keepH - seen.h, world.h - keepH),
  };
}

// Fit to width, but never below the label floor and never magnified past 100%. When the floor wins, the chart starts
// at the top-left and the reader pans.
export function fitView(world: Size, vp: Size): View {
  const k = Math.min(FIT_MAX, Math.max(FLOOR, vp.w / world.w));
  const spare = vp.w / k - world.w;
  return { k, x: spare > 0 ? -spare / 2 : 0, y: 0 };
}

export function viewBox(view: View, vp: Size): string {
  return `${view.x} ${view.y} ${vp.w / view.k} ${vp.h / view.k}`;
}

// Zoom so the world point under the pointer stays under it.
export function zoomAt(view: View, world: Size, vp: Size, k: number, px: number, py: number): View {
  const next = clamp(k, MIN_ZOOM, MAX_ZOOM);
  return clampView(
    { k: next, x: view.x + px / view.k - px / next, y: view.y + py / view.k - py / next },
    world,
    vp,
  );
}

export function panBy(view: View, world: Size, vp: Size, dx: number, dy: number): View {
  return clampView({ ...view, x: view.x - dx / view.k, y: view.y - dy / view.k }, world, vp);
}

export interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

export function visible(view: View, vp: Size, box: Box): boolean {
  return (
    box.x >= view.x &&
    box.y >= view.y &&
    box.x + box.width <= view.x + vp.w / view.k &&
    box.y + box.height <= view.y + vp.h / view.k
  );
}

// Center a box, never smaller than the label floor.
export function centerOn(view: View, world: Size, vp: Size, box: Box): View {
  const k = Math.max(view.k, FLOOR);
  return clampView(
    { k, x: box.x + box.width / 2 - vp.w / k / 2, y: box.y + box.height / 2 - vp.h / k / 2 },
    world,
    vp,
  );
}
