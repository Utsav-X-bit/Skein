import { clsx } from "clsx";

export interface WordmarkProps {
  className?: string;
  title?: string;
}

/** "SKEIN" wordmark: Geist Mono 600, 0.18em tracking (design spec 5.2). */
export function Wordmark({ className, title = "Skein" }: WordmarkProps) {
  return (
    <span
      className={clsx("select-none", className)}
      aria-label={title}
      style={{
        fontFamily:
          "'Geist Mono Variable', 'Geist Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
        fontWeight: 600,
        letterSpacing: "0.18em",
      }}
    >
      SKEIN
    </span>
  );
}

export default Wordmark;
