import { errText } from "@/lib/api";

export type Role = "read" | "mutate" | "secret";

/** The roles the helper accepts; the order is the capability order. */
export const Roles: Role[] = ["read", "mutate", "secret"];

export type RevokeConsequence = "last-grant" | "last-secret";

/**
 * revokeConsequence recognises the helper's refusal that only --force
 * overrides. The GUI must never pass force on its own: it asks again, naming
 * the consequence it read here, and only then retries with force.
 */
export function revokeConsequence(err: unknown): RevokeConsequence | null {
  const text = errText(err);
  if (!text.includes("--force")) return null;
  // "last secret grant" is the narrower refusal; test it first because the
  // broader sentence ("last grant") is a different consequence.
  if (text.includes("last secret grant")) return "last-secret";
  if (text.includes("last grant")) return "last-grant";
  return null;
}

/**
 * revokeArg is the identity form the helper takes on this platform. The
 * grants table shows the daemon's own key ("uid:1001"); the Unix verb takes a
 * bare positive integer, and Windows takes the SID or account name as-is.
 */
export function revokeArg(identity: string): string {
  const trimmed = identity.trim();
  return trimmed.startsWith("uid:") ? trimmed.slice(4) : trimmed;
}

/** consequenceText names what --force would do, in the caller's terms. */
export function consequenceText(c: RevokeConsequence): string {
  return c === "last-secret"
    ? "This is the last secret grant. Removing it leaves no granted user able to manage secrets or rotate the network secret; only root/SYSTEM keep implicit secret access."
    : "This is the last grant. Removing it denies every non-root caller until a new grant is added; root/SYSTEM keep implicit secret access.";
}
