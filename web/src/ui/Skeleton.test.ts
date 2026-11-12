import { describe, expect, it } from "vitest";

import { skeletonClass } from "./Skeleton";

describe("skeletonClass", () => {
  it("uses the small radius by default", () => {
    expect(skeletonClass("h-9 w-20")).toBe("animate-pulse bg-sunken rounded-sm h-9 w-20");
    expect(skeletonClass()).toBe("animate-pulse bg-sunken rounded-sm");
  });

  it("leaves the radius to a caller that sets one", () => {
    expect(skeletonClass("h-[22px] w-20 rounded-full")).toBe("animate-pulse bg-sunken h-[22px] w-20 rounded-full");
    expect(skeletonClass("rounded-card")).not.toContain("rounded-sm");
  });
});
