/** Mobile-web detection for the PWA "add to home screen" nudge. Hygur ships no
 *  native mobile app — the installable PWA IS the mobile app — so this drives the
 *  platform-specific install tutorial and the onboarding QR step. */

export type MobileOS = "ios" | "android";

/** The mobile OS of a WEB browser, or null on desktop. iPadOS 13+ reports as a
 *  Mac, so a touch-capable "Macintosh" is also treated as iOS. */
export function mobileOS(): MobileOS | null {
  if (typeof navigator === "undefined") return null;
  const ua = navigator.userAgent || "";
  if (/iPhone|iPad|iPod/.test(ua)) return "ios";
  if (/Macintosh/.test(ua) && typeof document !== "undefined" && "ontouchend" in document) {
    return "ios"; // iPadOS in desktop mode
  }
  if (/Android/.test(ua)) return "android";
  return null;
}

/** True when already launched as an installed PWA (home-screen app) — both the
 *  standard display-mode and the iOS-Safari legacy flag. */
export function isStandalone(): boolean {
  if (typeof window === "undefined") return false;
  const mm = window.matchMedia?.("(display-mode: standalone)").matches ?? false;
  const iosLegacy = (navigator as unknown as { standalone?: boolean }).standalone === true;
  return mm || iosLegacy;
}
