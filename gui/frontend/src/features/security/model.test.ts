import { describe, expect, it } from "vitest";
import { consequenceText, revokeArg, revokeConsequence } from "./model";

describe("revoke consequence", () => {
  it("recognises the last-grant refusal and only with --force", () => {
    expect(
      revokeConsequence(
        new Error('revoke-user: "1001" is the last grant in /etc/skein/access; re-run with --force (afterwards every non-root caller is denied until a new grant is added)'),
      ),
    ).toBe("last-grant");
  });

  it("recognises the narrower last-secret refusal", () => {
    expect(revokeConsequence('revoke-user: "1001" is the last secret grant in /etc/skein/access; re-run with --force')).toBe("last-secret");
  });

  it("leaves an unrelated refusal alone", () => {
    expect(revokeConsequence('revoke-user: "1001" has no grant in /etc/skein/access')).toBeNull();
    expect(revokeConsequence("forbidden: secret role required; ask an administrator to grant it")).toBeNull();
    expect(revokeConsequence(undefined)).toBeNull();
  });

  it("names a distinct consequence per case", () => {
    expect(consequenceText("last-grant")).toContain("every non-root caller");
    expect(consequenceText("last-secret")).toContain("last secret grant");
    expect(consequenceText("last-secret")).not.toBe(consequenceText("last-grant"));
  });
});

describe("revoke argument", () => {
  it("drops the daemon's uid: prefix for the Unix verb", () => {
    expect(revokeArg("uid:1001")).toBe("1001");
    expect(revokeArg("  uid:0 ")).toBe("0");
  });

  it("passes a Windows SID through unchanged", () => {
    expect(revokeArg("S-1-5-21-1-2-3-1001")).toBe("S-1-5-21-1-2-3-1001");
  });
});
