import { describe, expect, it } from "vitest";
import { actionOptions, auditQuery } from "./AuditLog";

describe("auditQuery", () => {
  it("leaves out empty filters", () => {
    expect(auditQuery({ action: "project.*", user_id: "", since: "2026-11-01", until: "" }, { before_id: "9" })).toBe(
      "action=project.*&since=2026-11-01&before_id=9",
    );
  });
});

describe("actionOptions", () => {
  it("adds one family choice per prefix", () => {
    expect(actionOptions(["project.deploy", "project.delete", "auth.login"]).map((o) => o.value)).toEqual([
      "auth.*",
      "auth.login",
      "project.*",
      "project.delete",
      "project.deploy",
    ]);
  });
});
