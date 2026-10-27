import { describe, expect, it } from "vitest";
import { withStagingNested, type Project } from "./projects";

const p = (name: string, staging_of?: string): Project =>
  ({ id: 0, name, source: "manual", auto_deploy: false, created_at: "", staging_of }) as Project;
const names = (list: Project[]) => withStagingNested(list).map((x) => x.name);

describe("withStagingNested", () => {
  it("puts a staging straight after its live project", () => {
    expect(names([p("app"), p("blog"), p("app-staging", "app")])).toEqual(["app", "app-staging", "blog"]);
  });

  it("keeps a staging whose live is not listed", () => {
    expect(names([p("app-staging", "app"), p("blog")])).toEqual(["app-staging", "blog"]);
  });
});
