import { Pill } from '../components/StatusBadge';
import { serviceStateMeta } from '../services';

// A service's state, in the same pill as a VM's status badge.
export function ServiceBadge({ state }: { state: string }) {
  return <Pill s={serviceStateMeta(state)} />;
}
