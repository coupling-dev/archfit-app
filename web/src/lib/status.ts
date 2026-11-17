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
  ['Done', { tone: 'success', icon: 'check-circle' }],
  ['On', { tone: 'success', icon: 'check-circle' }],
  ['Signed', { tone: 'success', icon: 'check-circle' }],
  ['Required', { tone: 'success', icon: 'check-circle' }],
  ['Policy approved', { tone: 'success', icon: 'check-circle' }],
  // Failing: the check did not pass.
  ['fail', { tone: 'danger', icon: 'x-circle' }],
  // The one repository state with colour: an expired exception fails every pull request.
  ['expired', { tone: 'danger', icon: 'x-circle' }],
  // Waiting on someone else.
  ['waiting', { tone: 'attention', icon: 'clock' }],
  ['Waiting', { tone: 'attention', icon: 'clock' }],
  ['Waiting for owner', { tone: 'attention', icon: 'clock' }],
  ['Waiting for installation approval', { tone: 'attention', icon: 'clock' }],
  // Needs an action from the reader.
  ['Action needed', { tone: 'attention', icon: 'alert' }],
  ['Not started', { tone: 'attention', icon: 'alert' }],
  ['Review needed', { tone: 'attention', icon: 'alert' }],
  ['Check with an admin', { tone: 'attention', icon: 'alert' }],
  ['Not required', { tone: 'attention', icon: 'alert' }],
  ['Could not confirm approval', { tone: 'attention', icon: 'alert' }],
  // In progress or ready for the reader to do: the next step is clear.
  ['Ready for review', { tone: 'accent', icon: 'info' }],
  ['First approval', { tone: 'accent', icon: 'info' }],
  ['Workflow review', { tone: 'accent', icon: 'info' }],
  ['Advisory', { tone: 'accent', icon: 'info' }],
  // Not known: never shown as a pass.
  ['unknown', { tone: 'neutral', icon: 'question' }],
  ['Check', { tone: 'neutral', icon: 'question' }],
  ['Verify yourself', { tone: 'neutral', icon: 'question' }],
  // Not applicable.
  ['Does not apply', { tone: 'neutral', icon: 'dot' }],
]);

const fallback: StatusStyle = { tone: 'neutral', icon: 'dot' };

export function statusStyle(label: string): StatusStyle {
  return styles.get(label) ?? fallback;
}
