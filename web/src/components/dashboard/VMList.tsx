import type { VM } from '../../types';
import { VMCard } from './VMCard';
import { VMTable } from './VMTable';
import type { VMStats } from './shared';

interface VMListProps {
  vms: VM[];
  layout: 'cards' | 'table';
  leaseMap: Record<string, string>;
  statsMap: Record<string, VMStats>;
  forceOffIds?: Set<string>;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onForceStop?: (id: string) => void;
  onDelete: (id: string) => void;
  onOpen: (id: string) => void;
  onOpenService?: (name: string) => void;
  // Inside a service's group a card does not repeat which service it is part of.
  inGroup?: boolean;
}

// VMList shows VMs as cards or a table, as the person chose.
export function VMList({
  vms,
  layout,
  leaseMap,
  statsMap,
  forceOffIds,
  onStart,
  onStop,
  onForceStop,
  onDelete,
  onOpen,
  onOpenService,
  inGroup = false,
}: VMListProps) {
  if (layout === 'table') {
    return (
      <VMTable
        vms={vms}
        leaseMap={leaseMap}
        forceOffIds={forceOffIds}
        onStart={onStart}
        onStop={onStop}
        onForceStop={onForceStop}
        onDelete={onDelete}
        onOpen={onOpen}
      />
    );
  }
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 360px), 1fr))',
        gap: 16,
      }}
    >
      {vms.map(vm => (
        <VMCard
          key={vm.id}
          vm={vm}
          stats={statsMap[vm.id]}
          ip={leaseMap[vm.name]}
          canForceStop={forceOffIds?.has(vm.id)}
          onStart={() => onStart(vm.id)}
          onStop={() => onStop(vm.id)}
          onForceStop={onForceStop ? () => onForceStop(vm.id) : undefined}
          onDelete={() => onDelete(vm.id)}
          onOpen={() => onOpen(vm.id)}
          onOpenService={!inGroup && vm.service && onOpenService ? () => onOpenService(vm.service!) : undefined}
          showService={!inGroup}
        />
      ))}
    </div>
  );
}
