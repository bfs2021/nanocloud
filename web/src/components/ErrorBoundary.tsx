import { Component, type ErrorInfo, type ReactNode } from 'react';

interface Props {
  children: ReactNode;
  /** Optional label shown in the fallback to help identify which section failed */
  label?: string;
  /**
   * For overlays (drawers, dialogs, the palette): the fallback is shown as a
   * dialog of its own, since the overlay's place in the page is not visible,
   * and closing it calls this to close the overlay.
   */
  onDismiss?: () => void;
}

interface State {
  hasError: boolean;
  message: string;
}

/**
 * Catches render-time errors in the subtree and shows a small inline fallback
 * instead of white-screening the entire app. Wrap any view that touches
 * potentially-undefined API data (ImagesView, Dashboard, VMDetail, etc.).
 */
export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, message: '' };
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, message: error.message };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack);
  }

  handleRetry = () => {
    this.setState({ hasError: false, message: '' });
  };

  handleDismiss = () => {
    this.setState({ hasError: false, message: '' });
    this.props.onDismiss?.();
  };

  render() {
    if (!this.state.hasError) {
      return this.props.children;
    }

    const label = this.props.label ?? 'This section';
    const overlay = !!this.props.onDismiss;

    const card = (
      <div
        role={overlay ? 'alertdialog' : undefined}
        aria-modal={overlay || undefined}
        aria-label={overlay ? `${label} ran into a problem` : undefined}
        style={{
          margin: overlay ? 0 : 24,
          padding: '20px 22px',
          background: overlay ? 'var(--nd-bg)' : 'var(--nd-danger-bg)',
          border: '1px solid var(--nd-danger-line)',
          borderRadius: 12,
          maxWidth: 540,
          boxShadow: overlay ? 'var(--nd-shadow-lg)' : undefined,
        }}
      >
        <div
          style={{
            fontWeight: 600,
            fontSize: 14,
            color: 'var(--nd-danger)',
            marginBottom: 6,
          }}
        >
          {label} ran into a problem
        </div>
        {this.state.message && (
          <div
            style={{
              fontFamily: 'var(--nd-font-mono)',
              fontSize: 11.5,
              color: 'var(--nd-danger)',
              background: 'var(--nd-danger-bg)',
              borderRadius: 7,
              padding: '7px 10px',
              marginBottom: 14,
              wordBreak: 'break-word',
            }}
          >
            {this.state.message}
          </div>
        )}
        <button
          onClick={overlay ? this.handleDismiss : this.handleRetry}
          autoFocus={overlay}
          style={{
            background: 'var(--nd-accent)',
            border: 'none',
            borderRadius: 8,
            padding: '7px 16px',
            fontSize: 13,
            fontWeight: 600,
            color: 'var(--nd-on-accent)',
            cursor: 'pointer',
          }}
        >
          {overlay ? 'Close' : 'Try again'}
        </button>
      </div>
    );

    if (!overlay) return card;
    return (
      <div
        onKeyDown={e => {
          if (e.key === 'Escape') this.handleDismiss();
        }}
        style={{
          position: 'fixed',
          inset: 0,
          zIndex: 60,
          background: 'var(--nd-scrim)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: 16,
        }}
      >
        {card}
      </div>
    );
  }
}
