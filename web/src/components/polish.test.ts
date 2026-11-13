import { describe, expect, it } from "vitest";
import { rollbackTarget, type Deployment } from "../api/deployments";
import { commitUrl, failureImpact } from "./OverviewTab";
import { diskParts } from "../pages/Dashboard";
import { usageTone } from "../ui/Meter";
import { formatStamp } from "../ui/format";

const dep = (number: number, status: string, extra: Partial<Deployment> = {}): Deployment => ({
  id: number,
  number,
  status,
  triggered_by: "test",
  created_at: "2026-11-11T21:40:00Z",
  ...extra,
});

describe("usageTone", () => {
  it("goes amber at 85 and red at 95", () => {
    expect(usageTone(84)).toBe("accent");
    expect(usageTone(85)).toBe("warn");
    expect(usageTone(95)).toBe("err");
  });
});

describe("rollbackTarget", () => {
  it("offers the success before the live one, skipping failures", () => {
    expect(rollbackTarget([dep(14, "failed"), dep(13, "succeeded"), dep(12, "succeeded")])?.number).toBe(12);
  });
  it("offers nothing with one success or while a deploy runs", () => {
    expect(rollbackTarget([dep(2, "failed"), dep(1, "succeeded")])).toBeUndefined();
    expect(rollbackTarget([dep(3, "pulling"), dep(2, "succeeded"), dep(1, "succeeded")])).toBeUndefined();
  });
});

describe("failureImpact", () => {
  const live = dep(13, "succeeded");
  it("says nothing changed only for steps before the containers are touched", () => {
    expect(failureImpact(dep(14, "failed", { failed_step: "pulling" }), live, "shop.example.com")).toBe(
      "The running containers did not change. #13 is still running and serving shop.example.com.",
    );
    expect(failureImpact(dep(14, "failed", { failed_step: "applying" }), live)).toMatch(/some may be on the new version/);
    expect(failureImpact(dep(14, "failed"), live)).toBe("#13 was the last deploy that succeeded.");
  });
  it("says so when nothing has ever succeeded", () => {
    expect(failureImpact(dep(1, "failed", { failed_step: "pulling" }), undefined)).toMatch(/No deploy/);
  });
});

describe("commitUrl", () => {
  it("links GitHub https remotes only", () => {
    expect(commitUrl("https://github.com/acme/shop.git", "a3f9c21")).toBe("https://github.com/acme/shop/commit/a3f9c21");
    expect(commitUrl("git@github.com:acme/shop.git", "a3f9c21")).toBeUndefined();
    expect(commitUrl("https://gitlab.com/acme/shop", "a3f9c21")).toBeUndefined();
  });
});

describe("diskParts", () => {
  const kind = (bytes: number) => ({ count: 1, active_count: 1, bytes, reclaimable_bytes: 0 });
  it("puts the host's remainder in other, never below zero", () => {
    const docker = { images: kind(40), containers: kind(5), volumes: kind(10), build_cache: kind(20) };
    const parts = diskParts({ host: { used_bytes: 100, total_bytes: 200 }, docker });
    expect(parts.map((p) => p.bytes)).toEqual([40, 20, 10, 30]);
    const small = diskParts({ host: { used_bytes: 50, total_bytes: 200 }, docker });
    expect(small[3].bytes).toBe(5);
  });
  it("is empty without Docker's figures", () => {
    expect(diskParts({ host: { used_bytes: 1, total_bytes: 2 } })).toEqual([]);
  });
});

describe("formatStamp", () => {
  it("writes DD/MM/YYYY HH:MM", () => {
    expect(formatStamp("2026-02-03T04:05:00")).toBe("03/02/2026 04:05");
    expect(formatStamp("")).toBe("");
  });
});
