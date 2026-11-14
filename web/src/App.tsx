import { useState, useEffect, useCallback, useRef } from 'react';
import './index.css';
import type { VM, StoragePool, HostBridge, NetworkLease, NanoEvent, ServiceRecipe } from './types';
import {
  listVMs,
  startVM,
  stopVM,
  deleteVM,
  listStoragePools,
  listNetworkInterfaces,
  listNetworkLeases,
  getVMStats,
  listEvents,
  deleteService,
  startService,
  stopService,
  setServiceShared,
  retryService,
  addServiceWorkers,
  removeServiceWorker,
  publishNodePort,
  unpublishNodePort,
  logout,
  UNAUTHORIZED_EVENT,
} from './api';
import { DEMO } from './demo';
import type { EventsPage } from './api';
import { Sidebar } from './components/Sidebar';
import { Dashboard } from './components/Dashboard';
import { ImagesView } from './components/ImagesView';
import { VMDetail } from './components/VMDetail';
import { CreateVMModal } from './components/CreateVMModal';
import { SettingsPage } from './components/SettingsPage';
import { ServicesView } from './components/ServicesView';
import { ServiceDetail } from './components/ServiceDetail';
import { ManagedDeleteDialog } from './components/ManagedDeleteDialog';
import { HostReadinessBanner } from './components/HostReadinessBanner';
import { DemoBanner } from './components/DemoBanner';
import { useConfirm } from './ui/ConfirmDialog';
import { Button } from './ui/Button';
import { ServiceCreateDrawer } from './components/ServiceCreateDrawer';
import { isLocalHostname } from './services';
import { ErrorBoundary } from './components/ErrorBoundary';
import { Toasts, useToasts } from './components/Toasts';
import type { View } from './types';
import { CapabilitiesContext } from './capabilities';
import { useTheme } from './theme';
import { useInterval } from './hooks/useInterval';
import { useHostStatus, useReadiness, useUpdateTag } from './hooks/useHost';
import { useImages } from './hooks/useImages';
import { useServices } from './hooks/useServices';
import { routeForView, useRoute } from './route';
import { CommandPalette, isPaletteShortcut, type Command } from './ui/CommandPalette';
import { Spinner } from './ui/Spinner';

interface StatsMap {
  [vmId: string]: { cpu_percent: number; memory_used_mb: number; memory_total_mb: number };
}

const EVENT_BACKLOG = 50;

// A cursor lower than the one we sent means the daemon restarted and its ring
// began again at seq 1. Appending would show every event twice, so the page
// replaces the backlog. Exported for tests.
export function mergeEventPage(prev: NanoEvent[], page: EventsPage, sentCursor: number): NanoEvent[] {
  if (page.cursor < sentCursor) return page.events.slice(-EVENT_BACKLOG);
  if (page.events.length === 0) return prev;
  return [...prev, ...page.events].slice(-EVENT_BACKLOG);
}

function without<T>(rec: Record<string, T>, key: string): Record<string, T> {
  const next = { ...rec };
  delete next[key];
  return next;
}

export default function App() {
  // Which page is showing lives in the URL (see route.ts), so Back and a
  // reload keep it.
  const [route, navigate] = useRoute();
  const view: View = route.view;
  const selectedVmId = route.view === 'detail' ? route.vm : null;
  const selectedService = route.view === 'service' ? route.service : null;
  const [showModal, setShowModal] = useState(false);
  // Kept here because Dashboard unmounts while a VM's detail view is open.
  const [vmLayout, setVmLayout] = useState<'cards' | 'table'>('cards');

  // Data
  const [vms, setVms] = useState<VM[]>([]);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [bridges, setBridges] = useState<HostBridge[]>([]);
  // The host's default-route interface — what a VLAN subinterface rides.
  // Empty when the host has none, the only case where the wizard offers no
  // VLAN at all.
  const [uplink, setUplink] = useState('');
  const [leases, setLeases] = useState<NetworkLease[]>([]);
  const [statsMap, setStatsMap] = useState<StatsMap>({});
  // The self-healing feed (GET /events), oldest-first, capped so a long
  // session's tab doesn't grow this without bound.
  const [events, setEvents] = useState<NanoEvent[]>([]);
  const eventsCursor = useRef(0);

  // Loading states
  const [vmsLoading, setVmsLoading] = useState(true);
  // A failed VM list fetch must not render as "No virtual machines yet" — that
  // makes a backend outage indistinguishable from an empty install.
  const [vmsError, setVmsError] = useState<string | null>(null);

  const { toasts, push, pushError, dismiss } = useToasts();
  const [theme, toggleTheme] = useTheme();
  const {
    hostname,
    cpuPercent: hostCpuPercent,
    memUsedMb: hostMemUsedMb,
    memTotalMb: hostMemTotalMb,
    cpus: hostCpus,
    capabilities,
    error: systemError,
    history: hostHistory,
  } = useHostStatus();
  const readinessChecks = useReadiness();
  const updateTag = useUpdateTag();
  const {
    cached: cachedImages,
    catalog,
    loadError: imagesError,
    pullProgress,
    pullErrors,
    refresh: fetchImages,
    pull: handlePullImage,
    cancel: handleCancelPullImage,
  } = useImages(pushError);
  const { services, setServices, error: servicesError, catalog: serviceCatalog, refresh: fetchServices } = useServices();

  const [launchRecipe, setLaunchRecipe] = useState<ServiceRecipe | null>(null);
  const [serviceBusy, setServiceBusy] = useState(false);
  const [guardedDelete, setGuardedDelete] = useState<VM | null>(null);
  const [confirm, confirmDialog] = useConfirm();
  const [paletteOpen, setPaletteOpen] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (isPaletteShortcut(e)) {
        e.preventDefault();
        setPaletteOpen(open => !open);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Graceful shutdowns this browser has requested, by VM id, with the time of
  // the request. The server can't report these, since the guest may ignore
  // them. Entries clear when a poll shows the VM stopped.
  const [stopRequests, setStopRequests] = useState<Record<string, number>>({});
  // Drives the force-off threshold; ticks only while a shutdown is outstanding.
  const [nowTick, setNowTick] = useState(() => Date.now());

  // Build lease map: vm_name -> ip
  const leaseMap: Record<string, string> = {};
  for (const l of leases) {
    leaseMap[l.vm_name] = l.ip;
  }

  // Overlay the client-side "stopping" phase onto VMs with an outstanding
  // graceful-shutdown request that libvirt still reports as running.
  const displayVms = vms.map(v => (stopRequests[v.id] && v.status === 'running' ? { ...v, phase: 'stopping' as const } : v));

  // A guest that hasn't honoured ACPI by now won't; offer force-off instead.
  const FORCE_OFF_AFTER_MS = 30_000;
  const forceOffIds = new Set(
    Object.entries(stopRequests)
      .filter(([, requestedAt]) => nowTick - requestedAt >= FORCE_OFF_AFTER_MS)
      .map(([id]) => id),
  );

  const fetchVMs = useCallback(async () => {
    try {
      const data = await listVMs();
      setVms(data);
      setVmsError(null);
      // The guest honoured the shutdown, so drop its "Stopping…" state.
      setStopRequests(prev => {
        const stillPending = Object.fromEntries(
          Object.entries(prev).filter(([id]) => data.some(v => v.id === id && v.status === 'running')),
        );
        return Object.keys(stillPending).length === Object.keys(prev).length ? prev : stillPending;
      });
    } catch (e) {
      setVmsError(e instanceof Error ? e.message : String(e));
    } finally {
      setVmsLoading(false);
    }
  }, []);

  const fetchMeta = useCallback(async () => {
    try {
      const [ps, ifaceResp, ls] = await Promise.all([listStoragePools(), listNetworkInterfaces(), listNetworkLeases()]);
      setPools(ps);
      setBridges(ifaceResp.bridges);
      setUplink(ifaceResp.uplink);
      setLeases(ls);
    } catch (e) {
      // These back the wizard's pool and network choices, which would
      // otherwise just look empty.
      pushError('Could not load storage pools and networks', e);
    }
  }, [pushError]);

  // Leases are the address the cards fall back to, so they are refreshed with
  // the VM list rather than read once at load.
  const fetchLeases = useCallback(async () => {
    try {
      setLeases(await listNetworkLeases());
    } catch (e) {
      console.error('Failed to fetch leases', e);
    }
  }, []);

  // Poll with the cursor last received — the ring is in-memory and resets on
  // daemon restart, but a stale cursor is replayed server-side, not blanked.
  const fetchEvents = useCallback(async () => {
    try {
      const sent = eventsCursor.current;
      const page = await listEvents(sent);
      eventsCursor.current = page.cursor;
      setEvents(prev => mergeEventPage(prev, page, sent));
    } catch (e) {
      console.error('Failed to fetch events', e);
    }
  }, []);

  const pollStats = useCallback(async () => {
    const running = vms.filter(v => v.status === 'running');
    if (running.length === 0) return;
    const results = await Promise.allSettled(
      running.map(async v => {
        const s = await getVMStats(v.id);
        return { id: v.id, stats: s };
      }),
    );
    setStatsMap(prev => {
      const next = { ...prev };
      for (const r of results) {
        if (r.status === 'fulfilled') {
          next[r.value.id] = r.value.stats;
        }
      }
      return next;
    });
  }, [vms]);

  useEffect(() => {
    fetchVMs();
    fetchMeta();
    fetchEvents();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Events share the VMs' 10s tick: a self-heal usually shows up as a VM state
  // change too.
  useInterval(() => {
    fetchVMs();
    fetchEvents();
    fetchLeases();
  }, 10000);

  // While a VM is booting, look every 3s instead, so its badge turns green
  // within moments of the guest being ready rather than on the next 10s tick.
  const anyBooting = vms.some(v => v.status === 'running' && v.phase === 'booting');
  useInterval(
    () => {
      fetchVMs();
      fetchLeases();
    },
    anyBooting ? 3000 : null,
  );

  // Tick while a graceful shutdown is outstanding so the force-off button
  // appears on time rather than on the next 10s VM poll.
  const hasPendingStops = Object.keys(stopRequests).length > 0;
  useInterval(() => setNowTick(Date.now()), hasPendingStops ? 2000 : null);

  // Stats for the running VMs' cards, only while the list is showing.
  useInterval(pollStats, view === 'dashboard' ? 5000 : null);

  async function handleStart(id: string) {
    try {
      await startVM(id);
      // Optimistically flip to "booting" — the real IP (and "ready" phase)
      // arrives on the next poll once DHCP hands out a lease.
      setVms(prev => prev.map(v => (v.id === id ? { ...v, status: 'running', phase: 'booting', uptime_seconds: 0 } : v)));
    } catch (e) {
      pushError(`Failed to start ${id}`, e);
    }
  }

  // handleStop requests a graceful shutdown without flipping the VM to
  // stopped: the guest may take 30s to honour ACPI, or ignore it. The VM shows
  // "Stopping…" until a poll reports otherwise.
  async function handleStop(id: string) {
    try {
      await stopVM(id);
      setStopRequests(prev => ({ ...prev, [id]: Date.now() }));
    } catch (e) {
      pushError(`Failed to stop ${id}`, e);
    }
  }

  // handleForceStop is the escape hatch for a guest that ignores ACPI — most
  // often a minimal image running no ACPI daemon at all.
  async function handleForceStop(id: string) {
    const ok = await confirm({
      title: `Force off ${id}?`,
      body: 'This is like pulling the power cord: anything the guest has not saved is lost.',
      confirmLabel: 'Force off',
    });
    if (!ok) return;
    try {
      await stopVM(id, true);
      setStopRequests(prev => without(prev, id));
      setVms(prev => prev.map(v => (v.id === id ? { ...v, status: 'stopped', phase: 'stopped', uptime_seconds: 0, ip: undefined } : v)));
    } catch (e) {
      pushError(`Failed to force off ${id}`, e);
    }
  }

  // A service's VM is not deleted on a plain confirm: ManagedDeleteDialog
  // points at the service first.
  async function handleDelete(id: string) {
    const vm = vms.find(v => v.id === id);
    if (vm?.service) {
      setGuardedDelete(vm);
      return;
    }
    const ok = await confirm({
      title: `Delete ${vm?.name ?? id}?`,
      body: 'Its disks and snapshots are deleted with it. This cannot be undone.',
      confirmLabel: 'Delete VM',
    });
    if (!ok) return;
    await deleteVMNow(id);
  }

  async function deleteVMNow(id: string) {
    try {
      await deleteVM(id);
      setVms(prev => prev.filter(v => v.id !== id));
      if (selectedVmId === id) navigate({ view: 'dashboard' }, { replace: true });
    } catch (e) {
      pushError(`Failed to delete ${id}`, e);
    }
  }

  function handleOpenDetail(id: string) {
    navigate({ view: 'detail', vm: id });
  }

  // A link names the VM; the daemon's id is its name, but mock data's is not.
  const selectedVM = displayVms.find(v => v.id === selectedVmId) ?? displayVms.find(v => v.name === selectedVmId) ?? null;

  const navView = view === 'detail' ? 'dashboard' : view === 'service' ? 'services' : view;

  function goTo(v: View) {
    navigate(routeForView(v));
  }

  function openService(name: string) {
    navigate({ view: 'service', service: name });
  }

  // Run a service action, keeping its buttons off until it lands and the list
  // reflects it.
  async function serviceAction(name: string, label: string, fn: (name: string) => Promise<void>) {
    setServiceBusy(true);
    try {
      await fn(name);
      await Promise.all([fetchServices(), fetchVMs()]);
      return true;
    } catch (e) {
      pushError(`Failed to ${label} ${name}`, e);
      return false;
    } finally {
      setServiceBusy(false);
    }
  }

  async function handleDeleteService(name: string) {
    const ok = await confirm({
      title: `Delete ${name}?`,
      body: 'Its VMs and everything stored on them are deleted too. This cannot be undone.',
      confirmLabel: 'Delete service',
    });
    if (!ok) return;
    if (await serviceAction(name, 'delete', deleteService)) {
      push('success', `Deleted ${name}.`);
      if (selectedService === name) navigate({ view: 'services' }, { replace: true });
    }
  }

  const selectedSvc = services.find(s => s.name === selectedService) ?? null;
  // A new service's VM takes its name, so both lists are off limits.
  const takenNames = new Set([...services.map(s => s.name), ...vms.map(v => v.name)]);

  // The gate listens for the same event a 401 raises, so signing out and
  // being signed out leave the app the same way.
  const signOut = async () => {
    try {
      await logout();
    } catch (e) {
      pushError('Could not sign out', e);
      return;
    }
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  };

  const commands: Command[] = [
    { id: 'new-vm', label: 'New VM', run: () => setShowModal(true) },
    { id: 'go-services', label: 'Go to Services', run: () => goTo('services') },
    { id: 'go-vms', label: 'Go to Virtual machines', run: () => goTo('dashboard') },
    ...serviceCatalog.map(r => ({ id: `launch-${r.id}`, label: `Launch ${r.name}`, hint: r.category, run: () => setLaunchRecipe(r) })),
    ...services.map(svc => ({
      id: `service-${svc.name}`,
      label: `Open service ${svc.name}`,
      hint: svc.recipe_name,
      run: () => openService(svc.name),
    })),
    { id: 'go-images', label: 'Go to Images', run: () => goTo('images') },
    { id: 'go-settings', label: 'Go to Settings', run: () => goTo('settings') },
    {
      id: 'theme',
      label: theme === 'light' ? 'Switch to dark theme' : 'Switch to light theme',
      run: toggleTheme,
    },
    { id: 'sign-out', label: 'Sign out', run: () => void signOut() },
    ...displayVms.flatMap(vm => {
      const ip = vm.ip || leaseMap[vm.name];
      const ssh = vm.username && ip ? `ssh ${vm.username}@${ip}` : '';
      const running = vm.status === 'running';
      const vmCommands: Command[] = [{ id: `open-${vm.id}`, label: `Open ${vm.name}`, hint: ip, run: () => handleOpenDetail(vm.id) }];
      if (ssh) {
        vmCommands.push({
          id: `ssh-${vm.id}`,
          label: `Copy SSH command for ${vm.name}`,
          hint: ssh,
          run: () => {
            navigator.clipboard.writeText(ssh).then(
              () => push('success', `Copied: ${ssh}`),
              e => pushError('Could not copy to the clipboard', e),
            );
          },
        });
      }
      vmCommands.push(
        running
          ? { id: `stop-${vm.id}`, label: `Stop ${vm.name}`, run: () => handleStop(vm.id) }
          : { id: `start-${vm.id}`, label: `Start ${vm.name}`, run: () => handleStart(vm.id) },
      );
      return vmCommands;
    }),
  ];

  const hostMemUsedGb = hostMemUsedMb / 1024;
  const hostMemTotalGb = hostMemTotalMb / 1024;

  return (
    <CapabilitiesContext.Provider value={capabilities}>
      <div className="nd-shell">
        <Sidebar
          view={navView}
          onNavigate={goTo}
          onOpenUpdates={() => navigate({ view: 'settings', section: 'updates' })}
          onNewVM={() => setShowModal(true)}
          cpuPercent={hostCpuPercent}
          memUsedGb={hostMemUsedGb}
          memTotalGb={hostMemTotalGb}
          hostname={hostname}
          cpuHistory={hostHistory}
          systemError={systemError}
          theme={theme}
          onToggleTheme={toggleTheme}
          services={services}
          currentService={view === 'service' ? selectedService : null}
          onOpenService={openService}
          updateTag={updateTag}
        />

        <main
          style={{
            flex: 1,
            minWidth: 0,
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {DEMO && <DemoBanner />}
          {/* On every page but the one that already lists the checks in full. */}
          {!(route.view === 'settings' && route.section === 'host') && (
            <HostReadinessBanner
              checks={readinessChecks}
              onOpenSettings={() => navigate({ view: 'settings', section: 'host' })}
              style={{ margin: '20px clamp(16px, 3vw, 40px) 0' }}
            />
          )}

          {vmsLoading && view === 'dashboard' && (
            <div
              style={{
                flex: 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: 'var(--nd-muted)',
                fontSize: 14,
                gap: 10,
              }}
            >
              <Spinner size={16} />
              Loading…
            </div>
          )}

          {!vmsLoading && view === 'dashboard' && (
            <ErrorBoundary label="Dashboard">
              <Dashboard
                vms={displayVms}
                vmsError={vmsError}
                events={events}
                forceOffIds={forceOffIds}
                onForceStop={handleForceStop}
                pools={pools}
                hostname={hostname}
                cpuPercent={hostCpuPercent}
                memUsedMb={hostMemUsedMb}
                memTotalMb={hostMemTotalMb}
                leaseMap={leaseMap}
                statsMap={statsMap}
                readinessChecks={readinessChecks}
                layout={vmLayout}
                onLayoutChange={setVmLayout}
                onStart={handleStart}
                onStop={handleStop}
                onDelete={handleDelete}
                onOpenDetail={handleOpenDetail}
                onNewVM={() => setShowModal(true)}
                onOpenSettings={() => navigate({ view: 'settings', section: 'host' })}
                onOpenImages={() => goTo('images')}
                cachedImageCount={cachedImages.length}
                onOpenPalette={() => setPaletteOpen(true)}
                onOpenService={openService}
                services={services}
              />
            </ErrorBoundary>
          )}

          {view === 'services' && (
            <ErrorBoundary label="Services">
              <ServicesView
                catalog={serviceCatalog}
                services={services}
                error={servicesError}
                hostname={hostname}
                onLaunch={setLaunchRecipe}
                onOpen={openService}
              />
            </ErrorBoundary>
          )}

          {view === 'service' && selectedSvc && (
            <ErrorBoundary label="Service">
              <ServiceDetail
                key={selectedSvc.name}
                svc={selectedSvc}
                events={events}
                busy={serviceBusy}
                hostname={hostname}
                localViewer={isLocalHostname(window.location.hostname)}
                onShare={shared =>
                  serviceAction(selectedSvc.name, shared ? 'share' : 'stop sharing', n => setServiceShared(n, shared).then(() => undefined))
                }
                onRetry={() => serviceAction(selectedSvc.name, 'retry', n => retryService(n).then(() => undefined))}
                onBack={() => goTo('services')}
                onStart={() => serviceAction(selectedSvc.name, 'start', startService)}
                onStop={() => serviceAction(selectedSvc.name, 'stop', stopService)}
                onDelete={() => handleDeleteService(selectedSvc.name)}
                onAddWorker={() => serviceAction(selectedSvc.name, 'add a worker to', n => addServiceWorkers(n).then(() => undefined))}
                onRemoveWorker={vm =>
                  serviceAction(selectedSvc.name, `remove ${vm} from`, n => removeServiceWorker(n, vm).then(() => undefined))
                }
                onPublish={(port, label, scheme) =>
                  serviceAction(selectedSvc.name, `publish ${label} of`, n => publishNodePort(n, port, label, scheme).then(() => undefined))
                }
                onUnpublish={port =>
                  serviceAction(selectedSvc.name, `unpublish port ${port} of`, n => unpublishNodePort(n, port).then(() => undefined))
                }
                onOpenVM={name => {
                  if (!vms.some(v => v.name === name)) {
                    push('error', `${name} has no VM yet — it appears once the service has created it.`);
                    return;
                  }
                  handleOpenDetail(name);
                }}
              />
            </ErrorBoundary>
          )}

          {view === 'images' && (
            <ErrorBoundary label="Images">
              <ImagesView
                cached={cachedImages}
                catalog={catalog}
                pullProgress={pullProgress}
                pullErrors={pullErrors}
                onPullImage={handlePullImage}
                onCancelPullImage={handleCancelPullImage}
                onRefresh={fetchImages}
                onError={pushError}
                loadError={imagesError}
              />
            </ErrorBoundary>
          )}

          {view === 'detail' && selectedVM && (
            <ErrorBoundary label="VM detail">
              <VMDetail
                vm={selectedVM}
                leases={leases}
                bridges={bridges}
                uplink={uplink}
                onBack={() => goTo('dashboard')}
                onStart={handleStart}
                onStop={handleStop}
                onForceStop={handleForceStop}
                onDelete={handleDelete}
                canForceStop={forceOffIds.has(selectedVM.id)}
                onError={pushError}
                onRefreshVMs={fetchVMs}
                onNotice={message => push('success', message)}
                onOpenService={openService}
                service={services.find(s => s.name === selectedVM.service)}
              />
            </ErrorBoundary>
          )}

          {view === 'detail' && !selectedVM && !vmsLoading && (
            <div style={{ padding: '60px clamp(16px, 3vw, 40px)', maxWidth: 560 }}>
              <h1 className="nd-settings-title">No VM named {selectedVmId}</h1>
              <p className="nd-settings-lede" style={{ marginBottom: 'var(--nd-space-5)' }}>
                It may have been deleted, or the link has a typo.
              </p>
              <Button onClick={() => goTo('dashboard')}>Show all VMs</Button>
            </div>
          )}

          {view === 'settings' && (
            <ErrorBoundary label="Settings">
              <SettingsPage section={route.view === 'settings' ? route.section : undefined} />
            </ErrorBoundary>
          )}
        </main>

        {showModal && (
          <ErrorBoundary label="New VM" onDismiss={() => setShowModal(false)}>
            <CreateVMModal
              catalog={catalog}
              pools={pools}
              bridges={bridges}
              uplink={uplink}
              readinessChecks={readinessChecks}
              hostCPUs={hostCpus}
              hostMemoryMB={hostMemTotalMb}
              pullProgress={pullProgress}
              pullErrors={pullErrors}
              onPullImage={handlePullImage}
              onCancelPullImage={handleCancelPullImage}
              onClose={() => setShowModal(false)}
              onCreated={() => {
                fetchImages();
                // Refetch now and a few more times while the VM boots, so its
                // status and IP appear before the slow background poll.
                fetchVMs();
                [2000, 5000, 10000].forEach(ms => setTimeout(fetchVMs, ms));
              }}
            />
          </ErrorBoundary>
        )}

        {launchRecipe && (
          <ErrorBoundary label={`Launch ${launchRecipe.name}`} onDismiss={() => setLaunchRecipe(null)}>
            <ServiceCreateDrawer
              recipe={launchRecipe}
              taken={takenNames}
              onClose={() => setLaunchRecipe(null)}
              onCreated={svc => {
                setLaunchRecipe(null);
                setServices(prev => [...prev.filter(s => s.name !== svc.name), svc]);
                openService(svc.name);
                fetchServices();
              }}
            />
          </ErrorBoundary>
        )}

        {confirmDialog}

        {guardedDelete && (
          <ErrorBoundary label="Delete VM" onDismiss={() => setGuardedDelete(null)}>
            <ManagedDeleteDialog
              vm={guardedDelete}
              service={services.find(s => s.name === guardedDelete.service)}
              onClose={() => setGuardedDelete(null)}
              onOpenService={() => {
                const name = guardedDelete.service!;
                setGuardedDelete(null);
                openService(name);
              }}
              onDeleteVM={() => {
                const id = guardedDelete.id;
                setGuardedDelete(null);
                deleteVMNow(id);
              }}
            />
          </ErrorBoundary>
        )}

        <ErrorBoundary label="Search" onDismiss={() => setPaletteOpen(false)}>
          <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
        </ErrorBoundary>
        <Toasts toasts={toasts} onDismiss={dismiss} />
      </div>
    </CapabilitiesContext.Provider>
  );
}
