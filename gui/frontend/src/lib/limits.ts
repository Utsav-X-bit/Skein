/**
 * Config bounds the daemon enforces, mirrored so a form can reject an
 * out-of-range value before the round trip.
 *
 * internal/conf/config.go is the authority: MinMTU = 576, and MaxMTU is
 * derived from the wire frame, not the port range —
 * MaxPayload(1400 − 26 header) − 12 nonce − 16 tag = **1346**. A form
 * that accepted 65535 handed the daemon a value it always rejects
 * ("node.mtu must be between 576 and 1346").
 *
 * Keep these in step with conf.MinMTU/conf.MaxMTU; if the daemon ever
 * reports its bounds through Capabilities, read them from there instead.
 */
export const PORT_MIN = 1;
export const PORT_MAX = 65535;
export const MTU_MIN = 576;
export const MTU_MAX = 1346;

export const portRangeMessage = `Port must be ${PORT_MIN}–${PORT_MAX}.`;
export const mtuRangeMessage = `MTU must be ${MTU_MIN}–${MTU_MAX}.`;

function inRange(value: string, min: number, max: number): boolean {
  const n = Number(value);
  return Number.isInteger(n) && n >= min && n <= max;
}

export function isValidPort(value: string): boolean {
  return inRange(value, PORT_MIN, PORT_MAX);
}

export function isValidMtu(value: string): boolean {
  return inRange(value, MTU_MIN, MTU_MAX);
}
