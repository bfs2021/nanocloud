// @novnc/novnc ships no type declarations, and the community
// @types/novnc__novnc package targets an older `lib/rfb` layout that no
// longer matches this package's `core/rfb.js` export — so this covers just
// the RFB surface Console.tsx actually uses.
declare module '@novnc/novnc' {
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: { wsProtocols?: string[] });
    scaleViewport: boolean;
    resizeSession: boolean;
    viewOnly: boolean;
    disconnect(): void;
  }
}
