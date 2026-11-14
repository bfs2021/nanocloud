import { useId } from 'react';
import type { CatalogImage, ImageState, VMTemplate } from '../../types';
import { formatBytes } from '../../design';
import { Button } from '../../ui/Button';
import { OsChip } from '../OsChip';
import { TemplatePicker } from '../TemplatePicker';
import './create-vm.css';

interface ImageStepProps {
  catalog: CatalogImage[];
  imageId: string;
  templateName: string;
  stateOf: (img: CatalogImage) => ImageState;
  progressOf: (img: CatalogImage) => number;
  errorOf: (img: CatalogImage) => string;
  onSelect: (imageId: string) => void;
  onTemplate: (t: VMTemplate | null) => void;
  onPull: (imageId: string) => void;
  onCancelPull: (imageId: string) => void;
}

// ImageStep needs only a *selected* image to continue. Downloading happens when
// the VM is created, so choosing never waits on a pull.
export function ImageStep({
  catalog,
  imageId,
  templateName,
  stateOf,
  progressOf,
  errorOf,
  onSelect,
  onTemplate,
  onPull,
  onCancelPull,
}: ImageStepProps) {
  const groupName = useId();
  const headingId = useId();

  return (
    <div className="cv-stack">
      <TemplatePicker selected={templateName} onSelect={onTemplate} />

      <div>
        <h3 id={headingId} className="cv-step-title">
          Base image
        </h3>
        <p className="cv-step-lede">Choose a cloud image to boot from — always the distro’s current release.</p>
      </div>

      {catalog.length === 0 && <p className="cv-note">No images are available yet. Check Settings for the host’s image cache.</p>}

      {/* A real radio group: Tab lands on the chosen card, the arrow keys move
          the choice, and the selected card is marked by more than colour. */}
      <div role="radiogroup" aria-labelledby={headingId} className="cv-images">
        {catalog.map(img => {
          const selected = imageId === img.id;
          const state = stateOf(img);
          const pct = progressOf(img);
          const err = errorOf(img);
          const inputId = `${groupName}-${img.id}`;
          const metaId = `${inputId}-meta`;

          return (
            <div key={img.id} className="cv-image" data-selected={selected}>
              <input
                id={inputId}
                className="cv-image-input"
                type="radio"
                name={groupName}
                checked={selected}
                aria-describedby={metaId}
                onChange={() => onSelect(img.id)}
              />
              <div className="cv-image-head">
                <OsChip os={img.os} size={30} />
                <label htmlFor={inputId} className="cv-image-name">
                  {img.name}
                </label>
                <span className="cv-image-check" aria-hidden="true">
                  ✓
                </span>
              </div>
              <div id={metaId} className="cv-image-meta">
                {img.arch ?? '—'} · {formatBytes(img.size_bytes)}
                {state === 'cached' && <span className="cv-image-cached"> · cached</span>}
              </div>

              {state === 'idle' && (
                <Button size="sm" variant="secondary" className="cv-image-action" onClick={() => onPull(img.id)}>
                  Download
                </Button>
              )}
              {state === 'pulling' && (
                <div className="cv-image-action">
                  <div
                    className="cv-progress"
                    role="progressbar"
                    aria-label={`Downloading ${img.name}`}
                    aria-valuenow={pct}
                    aria-valuemin={0}
                    aria-valuemax={100}
                  >
                    <div style={{ width: `${pct}%` }} />
                  </div>
                  <div className="cv-image-pulling">
                    <span>downloading… {pct}%</span>
                    <Button size="sm" variant="quiet" aria-label={`Stop downloading ${img.name}`} onClick={() => onCancelPull(img.id)}>
                      Stop
                    </Button>
                  </div>
                </div>
              )}
              {err && <div className="cv-image-error">{err}</div>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
