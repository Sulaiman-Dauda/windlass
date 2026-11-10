import { describe, expect, it } from "vitest";
import logsTabSource from "./LogsTab.tsx?raw";
import { appendLogLine, LOG_CAP, type LogBuffer } from "./LogsTab";

describe("appendLogLine", () => {
  it("keeps the count of received lines rising after the cap, while the kept lines stay at the cap", () => {
    let buf: LogBuffer = { lines: [], received: 0 };
    for (let i = 0; i < LOG_CAP; i++) buf = appendLogLine(buf, { stream: "stdout", text: `line ${i}` });
    expect(buf.lines.length).toBe(LOG_CAP);
    expect(buf.received).toBe(LOG_CAP);

    const before = buf.received;
    buf = appendLogLine(buf, { stream: "stdout", text: "newest" });
    expect(buf.lines.length).toBe(LOG_CAP);
    expect(buf.lines[buf.lines.length - 1].text).toBe("newest");
    expect(buf.received).toBe(before + 1);
  });

  it("numbers each line once, so a row's key survives older lines being dropped", () => {
    let buf: LogBuffer = { lines: [], received: 0 };
    for (let i = 0; i < LOG_CAP + 5; i++) buf = appendLogLine(buf, { stream: "stdout", text: `line ${i}` });
    expect(buf.lines[0]).toMatchObject({ seq: 6, text: "line 5" });
    expect(buf.lines[buf.lines.length - 1].seq).toBe(LOG_CAP + 5);
    expect(new Set(buf.lines.map((l) => l.seq)).size).toBe(LOG_CAP);
  });
});

describe("LogStream pane wiring", () => {
  it("drives TermPane's tick from the received count, not from the kept line count", () => {
    // TermPane only re-pins to the bottom when `tick` changes. A tick taken from
    // the kept lines stops changing at the cap, so the pane stops following.
    const tick = logsTabSource.match(/<TermPane\s+tick=\{([^}]*)\}/);
    expect(tick, "TermPane tick prop not found").not.toBeNull();
    expect(tick![1].trim()).not.toBe("lines.length");
    expect(tick![1].trim()).toBe("buf.received");
  });
});
