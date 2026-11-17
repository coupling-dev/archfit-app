// The icons the App draws. Each is one inline SVG in Icon.svelte; there is no icon font and no third-party icon set.
export const iconNames = [
  'check-circle',
  'x-circle',
  'clock',
  'alert',
  'question',
  'dot',
  'chevron',
  'link-external',
  'info',
] as const;

export type IconName = (typeof iconNames)[number];
