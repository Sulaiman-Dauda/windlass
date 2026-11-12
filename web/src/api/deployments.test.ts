import { describe, expect, it } from "vitest";
import {
  appendEvent,
  EVENT_CAP,
  paneTick,
  stageStates,
  summariseServices,
  type DeployEvent,
  type ServiceStatus,
} from "./deployments";

const step = (message: string, seq: number): DeployEvent => ({ seq, type: "step", message, ts: "" });

describe("stageStates", () => {
  const manual = [
    step("rendering environment and validating compose file", 1),
    step("pulling images", 2),
    step("starting services", 3),
    step("waiting for services to become healthy", 4),
    step("all services and application checks healthy", 5),
  ];

  it("marks a finished deployment's stages done or skipped", () => {
    expect(stageStates(manual, "succeeded")).toEqual({
      preparing: "done",
      syncing: "skipped",
      pulling: "done",
      building: "skipped",
      applying: "done",
      verifying: "done",
    });
  });

  it("marks the last stage reached as failed", () => {
    expect(stageStates(manual.slice(0, 3), "failed")).toMatchObject({
      pulling: "done",
      applying: "failed",
      verifying: "pending",
    });
  });

  it("marks the recorded failed step, even one that logged no step event", () => {
    // Pull started, then the build failed before it logged its step.
    expect(stageStates(manual.slice(0, 2), "failed", "building")).toMatchObject({
      pulling: "done",
      building: "failed",
      applying: "pending",
    });
    // An unknown step falls back to the events.
    expect(stageStates(manual.slice(0, 3), "failed", "seeding")).toMatchObject({ applying: "failed" });
  });

  it("follows the reported status while running", () => {
    expect(stageStates(manual.slice(0, 1), "pulling")).toMatchObject({
      preparing: "done",
      syncing: "skipped",
      pulling: "active",
      building: "pending",
    });
  });

  it("shows nothing started while queued", () => {
    expect(Object.values(stageStates([], "queued")).every((s) => s === "pending")).toBe(true);
  });
});

describe("summariseServices", () => {
  const svc = (state: string, health = "", exit_code = 0): ServiceStatus => ({
    service: "web",
    name: "p-web-1",
    state,
    health,
    exit_code,
    image: "nginx",
  });

  it("summarises", () => {
    expect(summariseServices([svc("running"), svc("running")])).toMatchObject({ tone: "ok", label: "Running", running: 2, total: 2 });
    expect(summariseServices([svc("running"), svc("exited", "", 137)])).toMatchObject({ tone: "warn", label: "Partial" });
    expect(summariseServices([svc("exited")])).toMatchObject({ tone: "idle", label: "Stopped" });
    expect(summariseServices([svc("running", "unhealthy")])).toMatchObject({ tone: "err", label: "Unhealthy" });
    expect(summariseServices([])).toMatchObject({ label: "Not running" });
    expect(summariseServices(undefined)).toBeNull();
  });

  it("leaves a run-once job that finished cleanly out of the count", () => {
    const migrate = svc("exited");
    expect(summariseServices([svc("running", "healthy"), svc("running", "healthy"), migrate])).toMatchObject({
      tone: "ok",
      label: "Running",
      running: 2,
      total: 2,
    });
    expect(summariseServices([svc("running"), svc("exited", "", 1)])).toMatchObject({ tone: "warn", label: "Partial", running: 1, total: 2 });
    expect(summariseServices([migrate, migrate])).toMatchObject({ tone: "idle", label: "Stopped", running: 0, total: 2 });
  });
});

describe("paneTick", () => {
  it("keeps rising after the log reaches its cap, so the pane keeps following", () => {
    const logLine = (seq: number): DeployEvent => ({ seq, type: "log", message: `line ${seq}`, ts: "" });
    let log: DeployEvent[] = [];
    for (let seq = 1; seq <= EVENT_CAP; seq++) log = appendEvent(log, logLine(seq));
    const atCap = paneTick(log);

    log = appendEvent(log, logLine(EVENT_CAP + 1));
    expect(log.length).toBe(EVENT_CAP);
    expect(log[0].seq).toBe(2);
    expect(paneTick(log)).toBeGreaterThan(atCap);
  });

  it("is 0 for an empty log", () => {
    expect(paneTick([])).toBe(0);
  });
});
