import type { ReactNode } from 'react';
import type { Service } from '../../types';
import { Button } from '../../ui/Button';
import { ServiceBadge } from '../../ui/ServiceBadge';
import { ServiceGlyph } from '../../ui/ServiceGlyph';
import { building as serviceBuilding, buildProgress, stuckMember } from '../../services';

interface ServiceGroupProps {
  name: string;
  // Undefined while the service list has not caught up with its VMs.
  service?: Service;
  onOpenService?: (name: string) => void;
  children: ReactNode;
}

export function ServiceGroup({ name, service: svc, onOpenService, children }: ServiceGroupProps) {
  // A service's VMs read "Running" well before the service is ready: say what
  // the service itself is still doing.
  const progress = svc && serviceBuilding(svc.state) ? buildProgress(svc) : null;
  const stuck = !!svc && !!stuckMember(svc.members);
  return (
    <div
      role="group"
      aria-label={`VMs of ${name}`}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        padding: 12,
        borderRadius: 16,
        border: '1px solid var(--nd-line)',
        background: 'var(--nd-surface-2)',
      }}
    >
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10, padding: '0 4px' }}>
        {svc && <ServiceGlyph recipe={svc.recipe} size={28} />}
        <span style={{ fontFamily: 'var(--nd-font-mono)', fontWeight: 600, fontSize: 14 }}>{name}</span>
        {svc && <span style={{ fontSize: 13, color: 'var(--nd-muted)' }}>{svc.recipe_name}</span>}
        {svc && <ServiceBadge state={svc.state} />}
        <span style={{ flex: 1 }} />
        {onOpenService && (
          <Button variant="quiet" size="sm" onClick={() => onOpenService(name)} style={{ color: 'var(--nd-accent)' }}>
            Open {name} →
          </Button>
        )}
      </div>
      {progress && (
        <p role="status" style={{ margin: '0 4px', fontSize: 12.5, lineHeight: 1.5, color: stuck ? 'var(--nd-danger)' : 'var(--nd-warn)' }}>
          {name} is not ready yet: {progress}. Its VMs run before it does.
        </p>
      )}
      {children}
    </div>
  );
}
