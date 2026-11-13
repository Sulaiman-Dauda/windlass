/** The live and staging pair the staging specs mock, and the routes every panel page reads. */
export const stagingProject = { id: 2, name: "app-staging", source: "git", git_repo: "https://example.com/app.git",
  git_branch: "preview", auto_deploy: true, staging_of: "app", created_at: "2026-10-27T00:00:00Z" };
export const liveProject = { ...stagingProject, id: 1, name: "app", staging_of: undefined, staging: "app-staging" };

/** The answer for a route outside the staging card, or undefined for the spec to answer. */
export function panelData(path: string, role: string, hasPassword = true): unknown {
  return path === "/auth/status" ? { authenticated: true, needs_setup: false, user: { id: 1, email: "member@example.com", role } }
    : path === "/auth/me" ? { id: 1, email: "member@example.com", role, has_password: hasPassword }
    : path === "/system/metrics" ? { node: { hostname: "test-server" }, containers: { running: 0, total: 0 } }
    : path === "/projects" ? [liveProject, stagingProject]
    : path === "/projects/app" ? liveProject
    : path === "/projects/app-staging" ? stagingProject
    : path.endsWith("/services") ? { services: [] }
    : path.endsWith("/domains") ? []
    : undefined;
}
