import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";

interface Connection {
  id: number;
  provider: string;
  name: string;
}

export default function Settings() {
  return (
    <div>
      <h1 className="text-xl font-semibold">Settings</h1>
      <div className="mt-6 max-w-2xl space-y-10">
        <SecuritySection />
        <GitConnections />
        <UsersSection />
        <UpdateSection />
      </div>
    </div>
  );
}

function SecuritySection() {
  const [enroll, setEnroll] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [code, setCode] = useState("");
  const qc = useQueryClient();
  const me = useQuery<{ totp_enabled: boolean }>({
    queryKey: ["auth", "me"],
    queryFn: () => api("/auth/me"),
  });

  const begin = useMutation({
    mutationFn: () =>
      api<{ secret: string; otpauth_url: string }>("/auth/totp/setup", { method: "POST" }),
    onSuccess: setEnroll,
  });
  const verify = useMutation({
    mutationFn: () =>
      api("/auth/totp/verify", { method: "POST", body: JSON.stringify({ code }) }),
    onSuccess: () => {
      setEnroll(null);
      setCode("");
      qc.invalidateQueries({ queryKey: ["auth", "me"] });
    },
  });
  const disable = useMutation({
    mutationFn: () => api("/auth/totp/disable", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "me"] }),
  });

  return (
    <section>
      <h2 className="text-base font-medium">Two-factor authentication</h2>
      {me.data?.totp_enabled ? (
        <div className="mt-2 flex items-center gap-4">
          <span className="text-sm text-emerald-400">TOTP is enabled</span>
          <button
            onClick={() => disable.mutate()}
            className="text-xs text-zinc-500 hover:text-red-400"
          >
            Disable
          </button>
        </div>
      ) : enroll ? (
        <div className="mt-3 space-y-3 rounded-md border border-zinc-900 p-4 text-sm">
          <p className="text-zinc-400">
            Add this secret to your authenticator app, then confirm a code:
          </p>
          <code className="block break-all rounded bg-zinc-900 p-2 font-mono text-xs">
            {enroll.secret}
          </code>
          <code className="block break-all rounded bg-zinc-900 p-2 font-mono text-xs text-zinc-500">
            {enroll.otpauth_url}
          </code>
          <div className="flex gap-2">
            <input
              inputMode="numeric"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              placeholder="123456"
              className="w-32 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-center font-mono text-sm text-zinc-100 outline-none"
            />
            <button
              onClick={() => verify.mutate()}
              disabled={code.length !== 6 || verify.isPending}
              className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 disabled:opacity-50"
            >
              Confirm
            </button>
          </div>
          {verify.isError && (
            <p className="text-red-400">
              {verify.error instanceof Error ? verify.error.message : "Invalid code"}
            </p>
          )}
        </div>
      ) : (
        <button
          onClick={() => begin.mutate()}
          className="mt-2 rounded-md border border-zinc-700 px-3 py-1.5 text-sm hover:bg-zinc-900"
        >
          Enable TOTP
        </button>
      )}
    </section>
  );
}

interface AdminUser {
  id: number;
  email: string;
  role: string;
  totp_enabled: boolean;
  oauth: string;
  disabled: boolean;
}

function UsersSection() {
  const qc = useQueryClient();
  const users = useQuery<AdminUser[]>({
    queryKey: ["users"],
    queryFn: () => api("/users"),
    retry: false, // 403 for non-admins
  });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("member");

  const create = useMutation({
    mutationFn: () =>
      api("/users", { method: "POST", body: JSON.stringify({ email, password, role }) }),
    onSuccess: () => {
      setEmail("");
      setPassword("");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/users/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });

  if (users.isError) return null; // not an admin

  return (
    <section>
      <h2 className="text-base font-medium">Users</h2>
      <form
        className="mt-3 flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <label className="flex-1">
          <span className="mb-1 block text-xs text-zinc-500">Email</span>
          <input
            required
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none"
          />
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Password (min 10)</span>
          <input
            type="password"
            minLength={10}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="empty = OAuth-only"
            className="w-44 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none"
          />
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Role</span>
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-sm text-zinc-100 outline-none"
          >
            <option value="viewer">viewer</option>
            <option value="member">member</option>
            <option value="admin">admin</option>
          </select>
        </label>
        <button
          type="submit"
          disabled={create.isPending}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 disabled:opacity-50"
        >
          Add
        </button>
      </form>
      {create.isError && (
        <p className="mt-2 text-sm text-red-400">
          {create.error instanceof Error ? create.error.message : "Failed"}
        </p>
      )}
      <div className="mt-3 space-y-2">
        {users.data?.map((u) => (
          <div
            key={u.id}
            className="flex items-center justify-between rounded-md border border-zinc-900 bg-zinc-900/40 px-4 py-2.5 text-sm"
          >
            <div>
              {u.email}
              <span className="ml-2 text-xs text-zinc-500">
                {u.role}
                {u.totp_enabled ? " · 2FA" : ""}
              </span>
            </div>
            <button
              onClick={() => {
                if (confirm(`Delete user ${u.email}?`)) remove.mutate(u.id);
              }}
              className="text-xs text-zinc-500 hover:text-red-400"
            >
              Remove
            </button>
          </div>
        ))}
      </div>
    </section>
  );
}

interface UpdateInfo {
  version: string;
  current_version: string;
  update_available: boolean;
}

function UpdateSection() {
  const check = useQuery<UpdateInfo>({
    queryKey: ["system", "update"],
    queryFn: () => api("/system/update"),
    retry: false,
  });
  const apply = useMutation({
    mutationFn: () => api("/system/update", { method: "POST" }),
  });

  if (check.isError) return null; // not admin, or offline

  return (
    <section>
      <h2 className="text-base font-medium">Updates</h2>
      <div className="mt-2 text-sm text-zinc-400">
        Running {check.data?.current_version ?? "…"}
        {check.data?.update_available ? (
          <span className="ml-3">
            <span className="text-amber-400">{check.data.version} available</span>
            <button
              onClick={() => apply.mutate()}
              disabled={apply.isPending}
              className="ml-3 rounded-md bg-zinc-100 px-3 py-1 text-xs font-medium text-zinc-900 disabled:opacity-50"
            >
              {apply.isPending ? "Updating…" : "Update now"}
            </button>
          </span>
        ) : (
          <span className="ml-2 text-zinc-600">— up to date</span>
        )}
      </div>
      {apply.isSuccess && (
        <p className="mt-2 text-sm text-emerald-400">
          Updating — the panel restarts in a few seconds. Deployed apps are unaffected.
        </p>
      )}
      {apply.isError && (
        <p className="mt-2 text-sm text-red-400">
          {apply.error instanceof Error ? apply.error.message : "Update failed"}
        </p>
      )}
    </section>
  );
}

function GitConnections() {
  const qc = useQueryClient();
  const connections = useQuery<Connection[]>({
    queryKey: ["git", "connections"],
    queryFn: () => api("/git/connections"),
  });

  const [provider, setProvider] = useState("github");
  const [name, setName] = useState("");
  const [token, setToken] = useState("");

  const add = useMutation({
    mutationFn: () =>
      api("/git/connections", {
        method: "POST",
        body: JSON.stringify({ provider, name, token }),
      }),
    onSuccess: () => {
      setName("");
      setToken("");
      qc.invalidateQueries({ queryKey: ["git", "connections"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) =>
      api(`/git/connections/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["git", "connections"] }),
  });

  return (
    <section>
      <h2 className="text-base font-medium">Git connections</h2>
      <p className="mt-1 text-sm text-zinc-500">
        Personal access tokens for private repositories. Stored encrypted;
        used only during git sync, never written to disk.
      </p>

      <form
        className="mt-4 flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}
      >
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Provider</span>
          <select
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
            className="rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          >
            <option value="github">GitHub</option>
            <option value="gitlab">GitLab</option>
          </select>
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Name</span>
          <input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="acme-bot"
            className="w-36 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <label className="flex-1">
          <span className="mb-1 block text-xs text-zinc-500">Token</span>
          <input
            required
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="ghp_… / glpat-…"
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <button
          type="submit"
          disabled={add.isPending}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          Add
        </button>
      </form>
      {add.isError && (
        <p className="mt-2 text-sm text-red-400">
          {add.error instanceof Error ? add.error.message : "Failed"}
        </p>
      )}

      <div className="mt-4 space-y-2">
        {connections.data?.map((c) => (
          <div
            key={c.id}
            className="flex items-center justify-between rounded-md border border-zinc-900 bg-zinc-900/40 px-4 py-2.5"
          >
            <div className="text-sm">
              {c.name}
              <span className="ml-2 text-xs text-zinc-500">{c.provider}</span>
            </div>
            <button
              onClick={() => remove.mutate(c.id)}
              className="text-xs text-zinc-500 hover:text-red-400"
            >
              Remove
            </button>
          </div>
        ))}
        {connections.data?.length === 0 && (
          <p className="text-sm text-zinc-600">No connections yet.</p>
        )}
      </div>
    </section>
  );
}
