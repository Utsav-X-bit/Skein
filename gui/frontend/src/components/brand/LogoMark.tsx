import "./skein-motion.css";

// The mark's aspect (width / height) comes from the trimmed source art.
const MARK_ASPECT = 1024 / 429;

export interface LogoMarkProps {
  /** Width in px; height follows the mark aspect. */
  size?: number;
  /** Reveals the mark (onboarding). */
  animated?: boolean;
  /** Idle opacity breathing. */
  idle?: boolean;
  className?: string;
  title?: string;
}

// The mark is a CSS mask filled with the current text colour, so it follows
// the theme without a second asset.
export function LogoMark({
  size = 96,
  animated = false,
  idle = false,
  className = "",
  title = "Skein",
}: LogoMarkProps) {
  return (
    <span
      role="img"
      aria-label={title}
      className={
        "skein-logo" +
        (animated ? " skein-logo--animated" : "") +
        (idle ? " skein-logo--idle" : "") +
        (className ? " " + className : "")
      }
      style={{ width: size, height: size / MARK_ASPECT }}
    />
  );
}

export default LogoMark;
