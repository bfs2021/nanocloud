import type { Service, VM } from '../../types';
import { ServiceGlyph } from '../../ui/ServiceGlyph';
import { Button } from '../../ui/Button';

// ServiceBanner says which service a VM belongs to and what it is there,
// so nobody mistakes it for a VM of their own.
export function ServiceBanner({ vm, service, onOpenService }: { vm: VM; service?: Service; onOpenService?: (name: string) => void }) {
  const name = vm.service!;
  const role = service?.members?.find(m => m.vm === vm.name)?.role_label;
  const many = (service?.members?.length ?? 1) > 1;
  return (
    <div
      role="note"
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        gap: 12,
        padding: '12px 16px',
        marginBottom: 20,
        borderRadius: 14,
        border: '1px solid var(--nd-accent-line)',
        background: 'var(--nd-accent-soft)',
      }}
    >
      {service && <ServiceGlyph recipe={service.recipe} size={32} />}
      <span style={{ flex: '1 1 320px', fontSize: 13.5, lineHeight: 1.5 }}>
        <b style={{ fontWeight: 600 }}>{many && role ? `A ${role.toLowerCase()} of ${name}` : `Runs ${name}`}</b>
        {service ? ` (${service.recipe_name})` : ''}. Use it like any VM; delete or resize it through {name}, which keeps the service whole.
      </span>
      {onOpenService && (
        <Button onClick={() => onOpenService(name)} style={{ borderColor: 'var(--nd-accent-line)' }}>
          Open {name} →
        </Button>
      )}
    </div>
  );
}
