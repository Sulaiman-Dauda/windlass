import { describe, expect, it } from "vitest";
import { elapsed, formatBytes, formatDuration, formatUptime, parseTime, plural, timeAgo } from "./format";

describe("parseTime", () => {
  it("reads ISO timestamps", () => {
    expect(parseTime("2026-10-04T18:13:00.000Z")?.toISOString()).toBe("2026-10-04T18:13:00.000Z");
  });
  it("treats SQLite's space-separated form as UTC", () => {
    expect(parseTime("2026-10-04 18:13:00")?.toISOString()).toBe("2026-10-04T18:13:00.000Z");
  });
  it("returns null for empty or invalid input", () => {
    expect(parseTime("")).toBeNull();
    expect(parseTime(undefined)).toBeNull();
    expect(parseTime("not a date")).toBeNull();
  });
});

describe("timeAgo", () => {
  const now = Date.parse("2026-10-04T12:00:00Z");
  it("is compact", () => {
    expect(timeAgo("2026-10-04T11:59:40Z", now)).toBe("just now");
    expect(timeAgo("2026-10-04T11:55:00Z", now)).toBe("5m ago");
    expect(timeAgo("2026-10-04T09:00:00Z", now)).toBe("3h ago");
    expect(timeAgo("2026-10-01T12:00:00Z", now)).toBe("3d ago");
  });
  it("never reports the future", () => {
    expect(timeAgo("2026-10-04T12:05:00Z", now)).toBe("just now");
  });
});

describe("formatDuration", () => {
  it("scales units", () => {
    expect(formatDuration(18_000)).toBe("18s");
    expect(formatDuration(125_000)).toBe("2m 05s");
    expect(formatDuration(3_720_000)).toBe("1h 02m");
  });
  it("rejects nonsense", () => {
    expect(formatDuration(-1)).toBe("");
  });
});

describe("elapsed", () => {
  it("measures between two timestamps", () => {
    expect(elapsed("2026-10-04T12:00:00Z", "2026-10-04T12:00:18Z")).toBe(18_000);
  });
  it("runs to now when unfinished", () => {
    expect(elapsed("2026-10-04T12:00:00Z", undefined, Date.parse("2026-10-04T12:01:00Z"))).toBe(60_000);
  });
});

describe("formatBytes", () => {
  it("uses binary units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(8.5 * 1024 ** 3)).toBe("8.5 GiB");
    expect(formatBytes(205.7 * 1024 ** 3)).toBe("206 GiB");
  });
});

describe("plural and uptime", () => {
  it("pluralises", () => {
    expect(plural(1, "project")).toBe("1 project");
    expect(plural(3, "project")).toBe("3 projects");
  });
  it("formats uptime", () => {
    expect(formatUptime(90_061)).toBe("1d 1h");
    expect(formatUptime(3_900)).toBe("1h 5m");
  });
});
