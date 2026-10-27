import { describe, expect, it } from "vitest";
import { diffLines } from "./diff";

const kinds = (before: string, after: string) => diffLines(before, after).map((l) => `${l.kind[0]}${l.text}`);

describe("diffLines", () => {
  it("marks only the changed lines", () => {
    expect(kinds("a\nb\nc\n", "a\nx\nc\n")).toEqual(["sa", "db", "ax", "sc"]);
  });

  it("handles an added service in the middle", () => {
    expect(kinds("services:\n  web:\n", "services:\n  worker:\n  web:\n")).toEqual(["sservices:", "a  worker:", "s  web:"]);
  });

  it("treats a missing side as empty", () => {
    expect(kinds("", "a\n")).toEqual(["aa"]);
    expect(kinds("a\n", "")).toEqual(["da"]);
    expect(kinds("", "")).toEqual([]);
  });

  it("falls back to whole files past the limit", () => {
    expect(diffLines("a\nb", "a\nc", 1).map((l) => l.kind)).toEqual(["del", "del", "add", "add"]);
  });
});
