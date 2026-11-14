import { useCallback, useEffect, useState } from 'react';
import { listServiceCatalog, listServices } from '../api';
import { building } from '../services';
import type { Service, ServiceRecipe } from '../types';
import { useInterval } from './useInterval';

// useServices polls the services like the VMs. The catalog is fixed for the
// life of the daemon, so it is read once.
export function useServices() {
  const [services, setServices] = useState<Service[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [catalog, setCatalog] = useState<ServiceRecipe[]>([]);

  const refresh = useCallback(async () => {
    try {
      setServices(await listServices());
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    listServiceCatalog().then(setCatalog, () => setCatalog([]));
    void refresh();
  }, [refresh]);

  // A service being built moves a step every few seconds to minutes; look
  // every 3s while one is, and every 10s otherwise.
  const busy = services.some(s => building(s.state) || s.state === 'starting');
  useInterval(refresh, busy ? 3000 : 10000);

  return { services, setServices, error, catalog, refresh };
}
