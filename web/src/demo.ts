// DEMO is the public demo build (npm run build:demo): mock data and no daemon
// to reach at all, so the screens only a live VM can fill say so instead of
// trying.
export const DEMO = import.meta.env.VITE_MOCK === 'true' && import.meta.env.VITE_DEMO === 'true';
