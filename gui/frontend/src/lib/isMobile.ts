import { useEffect, useState } from "react";

/**
 * isMobileEnvironment decides whether the mobile shell owns the
 * window. On Android it is always the mobile shell; on desktop the
 * existing AppShell stays. Detection is a coarse pointer (touch
 * primary) or an Android user agent, so a desktop browser never
 * takes the mobile path and a phone WebView always does.
 */
export function isMobileEnvironment(): boolean {
  if (typeof window === "undefined" || typeof navigator === "undefined") return false;
  if (/Android/i.test(navigator.userAgent)) return true;
  return window.matchMedia("(pointer: coarse)").matches && window.innerWidth < 800;
}

export function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(() => isMobileEnvironment());
  useEffect(() => {
    const onChange = () => setMobile(isMobileEnvironment());
    window.addEventListener("resize", onChange);
    return () => window.removeEventListener("resize", onChange);
  }, []);
  return mobile;
}
