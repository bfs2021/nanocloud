import React from 'react';
import ReactDOM from 'react-dom/client';
// Bundled into the binary: a self-hosted tool should not fetch its fonts
// from a CDN, and on an offline LAN it could not.
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import App from './App';
import { AuthGate } from './components/AuthGate';
import { applyTheme, followSystemTheme, storedTheme } from './theme';

// Before the first render, so the page never flashes the other theme.
applyTheme(storedTheme());
followSystemTheme();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <AuthGate>
      <App />
    </AuthGate>
  </React.StrictMode>,
);
