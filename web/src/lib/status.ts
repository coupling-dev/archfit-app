import type { IconName } from '../components/ui/icon-names';

export type Tone = 'success' | 'attention' | 'danger' | 'neutral' | 'accent' | 'done';
export interface StatusStyle {
  tone: Tone;
  icon: IconName;
}

// The one table of status words. A word is looked up exactly, as the App renders it (case included), so a word that
// is not here is neutral with a dot: it never reads as a pass. Labels keep their copy; only the tone and icon live here.
const styles = new Map<string, StatusStyle>([
  // Done, on, and passing: the check is met.
  ['pass', { tone: 'success', icon: 'check-circle' }],
  ['done', { tone: 'success', icon: 'check-circle' }],
  ['on', { tone: 'success', icon: 'check-circle' }],
  // Failing: the check did not pass, or a repository's state is expired (every pull request fails).
  ['fail', { tone: 'danger', icon: 'x-circle' }],
  ['expired', { tone: 'danger', icon: 'x-circle' }],
  // Waiting on someone else.
  ['waiting', { tone: 'attention', icon: 'clock' }],
  ['Waiting', { tone: 'attention', icon: 'clock' }],
  ['Waiting for owner', { tone: 'attention', icon: 'clock' }],
  // Needs an action from the reader.
  ['to do', { tone: 'attention', icon: 'alert' }],
  ['off', { tone: 'attention', icon: 'alert' }],
  // archfit/gate on the list: required is the one success, not required is a gap, not confirmed is a question.
  ['required', { tone: 'success', icon: 'check-circle' }],
  ['not required', { tone: 'attention', icon: 'alert' }],
  ['not confirmed', { tone: 'neutral', icon: 'question' }],
  // In progress or ready for the reader to do: the next step is clear.
  // Optional: the reader may choose; this is not an action and does not share the attention tone.
  ['optional', { tone: 'neutral', icon: 'dot' }],
  // Not known: never shown as a pass, and never inert.
  ['unknown', { tone: 'attention', icon: 'question' }],
  ['verify in GitHub', { tone: 'neutral', icon: 'question' }],
  // Not applicable.
  ['does not apply', { tone: 'neutral', icon: 'dot' }],
]);

const fallback: StatusStyle = { tone: 'neutral', icon: 'dot' };

export function statusStyle(label: string): StatusStyle {
  return styles.get(label) ?? fallback;
}
