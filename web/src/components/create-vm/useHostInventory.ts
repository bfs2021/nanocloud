import { useEffect, useState } from 'react';
import type { HostDeviceReport } from '../../types';
import { getHostDevices, getSystemInfo } from '../../api';

// useHostInventory fetches the two host facts only an advanced section needs,
// once that section is open: the device inventory costs a libvirt round-trip
// per node device. A known CPU count (knownCPUs > 0) skips that fetch.
export function useHostInventory({ wantCPUs, wantDevices, knownCPUs }: { wantCPUs: boolean; wantDevices: boolean; knownCPUs: number }) {
  const [fetchedCPUs, setFetchedCPUs] = useState(0);
  const [devices, setDevices] = useState<HostDeviceReport | null>(null);

  useEffect(() => {
    if (!wantCPUs || knownCPUs || fetchedCPUs) return;
    getSystemInfo()
      .then(info => setFetchedCPUs(info.cpus))
      .catch(() => {});
  }, [wantCPUs, knownCPUs, fetchedCPUs]);

  useEffect(() => {
    if (!wantDevices || devices) return;
    getHostDevices()
      .then(setDevices)
      // A host whose inventory cannot be read is not a reason to block a
      // create: the picker shows its own loading state, and the backend
      // refuses any device it cannot verify anyway.
      .catch(() => {});
  }, [wantDevices, devices]);

  return { hostCPUs: knownCPUs || fetchedCPUs, devices };
}
