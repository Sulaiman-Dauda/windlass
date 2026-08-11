import { describe, expect, it } from "vitest";

import { cn } from "./cn";

describe("cn", () => {
  it("joins enabled class names and ignores empty conditions", () => {
    expect(cn("button", false, null, undefined, "button-primary")).toBe(
      "button button-primary",
    );
  });
});
