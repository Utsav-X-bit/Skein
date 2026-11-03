import { useEffect, useRef } from "react";
import QRCode from "qrcode";

export function QRCodeView({ value, size = 160 }: { value: string; size?: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    if (!ref.current) return;
    QRCode.toCanvas(ref.current, value, { width: size, margin: 1, color: { dark: "#000000", light: "#ffffff" } }).catch(() => {});
  }, [value, size]);
  return <canvas ref={ref} width={size} height={size} className="rounded-md border border-[var(--border)] bg-white p-2" aria-label="Invite QR code" />;
}
