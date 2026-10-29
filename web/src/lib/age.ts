// How long ago a time was, in words: "just now", "5 minutes ago", "1 hour ago", "3 days ago". A time that cannot be
// read has no age, and a time ahead of now is just now: a clock that differs by a few seconds must not show a future.

const steps: [seconds: number, unit: string][] = [
  [86400, 'day'],
  [3600, 'hour'],
  [60, 'minute'],
];

export function ageWord(iso: string, now: number = Date.now()): string {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return '';
  const elapsed = Math.floor((now - at) / 1000);
  for (const [seconds, unit] of steps) {
    if (elapsed >= seconds) {
      const n = Math.floor(elapsed / seconds);
      return `${n} ${unit}${n === 1 ? '' : 's'} ago`;
    }
  }
  return 'just now';
}
