import { describe, expect, it } from "vitest";
import { nextAtBottom, onPaneScroll, type ScrollState } from "./TermPane";

// Geometry: a 600px viewport over 8503px of output.
const view = (scrollTop: number, scrollHeight = 8503) => ({ scrollTop, clientHeight: 600, scrollHeight });

describe("nextAtBottom", () => {
  it("keeps following when lines arrive while pinned to the bottom", () => {
    // Pinned at 7903; a batch of lines grows scrollHeight by 97px before the
    // scroll event from our own scrollTop assignment is handled.
    expect(nextAtBottom(true, 7903, view(7903, 8600))).toBe(true);
  });

  it("stops following when the reader scrolls up away from the bottom", () => {
    expect(nextAtBottom(true, 7903, view(7000))).toBe(false);
  });

  it("resumes following when the reader scrolls back to the bottom", () => {
    expect(nextAtBottom(false, 7000, view(7903))).toBe(true);
  });

  it("does not change state on a downward scroll that is still short of the bottom", () => {
    expect(nextAtBottom(false, 2000, view(2225))).toBe(false);
  });
});

describe("onPaneScroll event sequence", () => {
  // Drives the same handler the component's onScroll calls, one event at a time.
  it("follows output, stops on a user scroll up, and resumes at the bottom", () => {
    const state: ScrollState = { atBottom: true, lastScrollTop: 7903 };

    // Output grows 97px under the pinned pane; scrollTop has not moved yet.
    onPaneScroll(state, view(7903, 8600));
    expect(state.atBottom).toBe(true);

    // Our own scrollTop = scrollHeight assignment clamps to 8000 and fires a scroll.
    onPaneScroll(state, view(8000, 8600));
    expect(state.atBottom).toBe(true);

    // Reader scrolls up; lines keep arriving and the pane must not follow.
    onPaneScroll(state, view(7000, 8700));
    expect(state.atBottom).toBe(false);
    onPaneScroll(state, view(7000, 8800));
    expect(state.atBottom).toBe(false);

    // Reader scrolls back to the bottom; following resumes.
    onPaneScroll(state, view(8200, 8800));
    expect(state.atBottom).toBe(true);
    expect(state.lastScrollTop).toBe(8200);
  });
});
