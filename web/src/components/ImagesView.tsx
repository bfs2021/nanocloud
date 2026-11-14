import type { CachedImage, CatalogImage } from '../types';
import { formatBytes, formatDate, surfaceCard } from '../design';
import { OsChip } from './OsChip';
import { deleteImage } from '../api';
import { Spinner } from '../ui/Spinner';
import { useConfirm } from '../ui/ConfirmDialog';

interface ImagesViewProps {
  cached: CachedImage[];
  catalog: CatalogImage[];
  // Shared with CreateVMModal and owned by App — a download started in one
  // view stays visible in the other, and survives this view unmounting.
  pullProgress: Record<string, number>;
  pullErrors: Record<string, string>;
  onPullImage: (imageId: string) => void;
  onCancelPullImage: (imageId: string) => void;
  onRefresh: () => void;
  onError: (msg: string, e: unknown) => void;
  // Why the last refresh failed; the lists shown are from before it.
  loadError?: string | null;
}

export function ImagesView({
  cached,
  catalog,
  pullProgress,
  pullErrors,
  onPullImage,
  onCancelPullImage,
  onRefresh,
  onError,
  loadError,
}: ImagesViewProps) {
  const totalCachedBytes = cached.reduce((a, i) => a + (i.size_bytes ?? 0), 0);

  const [confirm, confirmDialog] = useConfirm();

  async function handleDelete(img: CachedImage) {
    const ok = await confirm({
      title: `Delete ${img.name}?`,
      body: 'Creating a VM from it again downloads it first.',
      confirmLabel: 'Delete image',
    });
    if (!ok) return;
    try {
      await deleteImage(img.id);
      onRefresh();
    } catch (e) {
      onError(`Failed to delete ${img.name}`, e);
    }
  }

  const monoLabel: React.CSSProperties = {
    fontFamily: 'var(--nd-font-mono)',
    fontSize: 11,
    letterSpacing: '0.05em',
    textTransform: 'uppercase',
    color: 'var(--nd-muted)',
    marginBottom: 12,
  };

  return (
    <div style={{ padding: '30px clamp(16px, 3vw, 40px) 60px', maxWidth: 1100, width: '100%' }}>
      {confirmDialog}
      <h1
        style={{
          margin: '0 0 5px',
          fontSize: 24,
          fontWeight: 600,
          letterSpacing: '-0.01em',
        }}
      >
        Images
      </h1>
      <p style={{ margin: '0 0 26px', color: 'var(--nd-text-2)', fontSize: 13.5 }}>Cloud images cached locally and available to pull.</p>

      {loadError && (
        <p
          role="alert"
          className="nd-field-error"
          style={{
            margin: '0 0 20px',
            padding: '10px 14px',
            borderRadius: 'var(--nd-radius-md)',
            border: '1px solid var(--nd-danger-line)',
            background: 'var(--nd-danger-bg)',
          }}
        >
          Couldn't load images: {loadError}
        </p>
      )}

      {/* Cached section */}
      <div style={monoLabel}>
        Cached ·{' '}
        <span style={{ color: 'var(--nd-text-2)' }}>
          {cached.length} images · {formatBytes(totalCachedBytes)}
        </span>
      </div>

      {cached.length === 0 ? (
        <div
          style={{
            background: 'var(--nd-surface)',
            border: '1px solid var(--nd-line)',
            borderRadius: 14,
            padding: '24px',
            color: 'var(--nd-muted)',
            fontSize: 13,
            textAlign: 'center',
            marginBottom: 34,
          }}
        >
          No images cached yet. Pull one from the catalog below.
        </div>
      ) : (
        <div
          style={{
            ...surfaceCard,
            overflow: 'hidden',
            marginBottom: 34,
          }}
        >
          {cached.map(img => (
            <div
              key={img.id}
              style={{
                display: 'grid',
                gridTemplateColumns: '1.6fr .7fr 1fr .8fr',
                gap: 12,
                padding: '14px 20px',
                borderBottom: '1px solid var(--nd-line-soft)',
                alignItems: 'center',
                fontSize: 13,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
                <OsChip os={img.os} size={34} />
                <div>
                  <div
                    style={{
                      fontWeight: 600,
                      fontFamily: 'var(--nd-font-mono)',
                      fontSize: 12.5,
                    }}
                  >
                    {img.name.toLowerCase().replace(/\s/g, '-')}.qcow2
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--nd-muted)' }}>{img.name}</div>
                </div>
              </div>
              <div
                style={{
                  fontFamily: 'var(--nd-font-mono)',
                  fontSize: 12,
                  color: 'var(--nd-text-2)',
                }}
              >
                {formatBytes(img.size_bytes)}
              </div>
              <div
                style={{
                  fontFamily: 'var(--nd-font-mono)',
                  fontSize: 12,
                  color: 'var(--nd-muted)',
                }}
              >
                {formatDate(img.cached_at)}
              </div>
              <div style={{ textAlign: 'right' }}>
                <button
                  onClick={() => handleDelete(img)}
                  style={{
                    background: 'var(--nd-surface)',
                    border: '1px solid var(--nd-line)',
                    borderRadius: 7,
                    padding: '5px 11px',
                    fontSize: 12,
                    color: 'var(--nd-text-2)',
                    cursor: 'pointer',
                  }}
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Catalog section */}
      <div style={monoLabel}>Catalog</div>
      <p style={{ margin: '-6px 0 14px', color: 'var(--nd-muted)', fontSize: 12.5 }}>
        Every image here always resolves to the distro's current release at pull time — no stale point releases.
      </p>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))',
          gap: 14,
        }}
      >
        {catalog.map(img => {
          const isPulling = pullProgress[img.id] !== undefined;
          const isCached = img.state === 'cached' && !isPulling;
          const progress = pullProgress[img.id] ?? img.progress ?? 0;
          const errMsg = pullErrors[img.id];

          return (
            <div
              key={img.id}
              style={{
                background: 'var(--nd-surface)',
                border: '1px solid var(--nd-line)',
                borderRadius: 13,
                padding: 16,
                boxShadow: 'var(--nd-shadow)',
              }}
            >
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                  marginBottom: isPulling ? 14 : 0,
                }}
              >
                <OsChip os={img.os} size={38} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontWeight: 600, fontSize: 14 }}>{img.name}</div>
                  <div
                    style={{
                      fontFamily: 'var(--nd-font-mono)',
                      fontSize: 11.5,
                      color: 'var(--nd-muted)',
                    }}
                  >
                    {img.arch ?? '—'} · {formatBytes(img.size_bytes)}
                  </div>
                </div>
                {isCached && (
                  <span
                    style={{
                      fontSize: 12,
                      color: 'var(--nd-ready)',
                      display: 'flex',
                      alignItems: 'center',
                      gap: 5,
                      fontWeight: 500,
                    }}
                  >
                    <span
                      style={{
                        width: 6,
                        height: 6,
                        background: 'var(--nd-ready)',
                        borderRadius: '50%',
                        display: 'inline-block',
                      }}
                    />
                    Cached
                  </span>
                )}
                {!isCached && !isPulling && (
                  <button
                    onClick={() => onPullImage(img.id)}
                    style={{
                      background: 'var(--nd-accent-soft)',
                      border: '1px solid var(--nd-accent-line)',
                      borderRadius: 8,
                      padding: '6px 14px',
                      fontSize: 12.5,
                      fontWeight: 600,
                      color: 'var(--nd-accent)',
                      cursor: 'pointer',
                      flexShrink: 0,
                    }}
                  >
                    Pull
                  </button>
                )}
              </div>

              {isPulling && (
                <div>
                  <div
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      fontSize: 11.5,
                      marginBottom: 6,
                      fontFamily: 'var(--nd-font-mono)',
                    }}
                  >
                    <span
                      style={{
                        color: 'var(--nd-accent)',
                        display: 'flex',
                        alignItems: 'center',
                        gap: 6,
                      }}
                    >
                      <Spinner size={11} track="var(--nd-accent-line)" />
                      Pulling…
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ color: 'var(--nd-text-2)' }}>{Math.round(progress)}%</span>
                      <button
                        onClick={() => onCancelPullImage(img.id)}
                        style={{
                          background: 'var(--nd-surface)',
                          border: '1px solid var(--nd-line)',
                          borderRadius: 6,
                          padding: '3px 9px',
                          fontSize: 11.5,
                          color: 'var(--nd-text-2)',
                          cursor: 'pointer',
                          fontFamily: 'var(--nd-font-mono)',
                          letterSpacing: '0.01em',
                          transition: 'border-color 0.15s, color 0.15s',
                        }}
                        onMouseEnter={e => {
                          (e.currentTarget as HTMLButtonElement).style.borderColor = 'var(--nd-danger-line)';
                          (e.currentTarget as HTMLButtonElement).style.color = 'var(--nd-danger)';
                        }}
                        onMouseLeave={e => {
                          (e.currentTarget as HTMLButtonElement).style.borderColor = 'var(--nd-line)';
                          (e.currentTarget as HTMLButtonElement).style.color = 'var(--nd-text-2)';
                        }}
                      >
                        Stop
                      </button>
                    </div>
                  </div>
                  <div
                    style={{
                      height: 6,
                      background: 'var(--nd-surface-2)',
                      borderRadius: 4,
                      overflow: 'hidden',
                    }}
                  >
                    <div
                      style={{
                        width: `${progress}%`,
                        height: '100%',
                        background: 'var(--nd-accent)',
                        borderRadius: 4,
                        transition: 'width 0.3s ease',
                      }}
                    />
                  </div>
                </div>
              )}
              {errMsg && <div style={{ fontSize: 11.5, color: 'var(--nd-danger)', marginTop: 8 }}>{errMsg}</div>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
