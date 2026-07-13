import { useState } from "react";
import { useSetup } from "../api/auth";

// First-run flow: the server prints a one-time setup token to its log; the
// admin pastes it here to claim the instance.
export default function Setup() {
  const [token, setToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const setup = useSetup();

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <form
        className="w-full max-w-sm space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          setup.mutate({ token, email, password });
        }}
      >
        <div className="text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-100">
            Welcome to Windlass
          </h1>
          <p className="mt-1 text-sm text-zinc-500">
            Create the admin account. The setup token is in the server log
            (printed at first start).
          </p>
        </div>

        <label className="block">
          <span className="mb-1 block text-sm text-zinc-400">Setup token</span>
          <input
            required
            value={token}
            onChange={(e) => setToken(e.target.value)}
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 font-mono text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>

        <label className="block">
          <span className="mb-1 block text-sm text-zinc-400">Email</span>
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>

        <label className="block">
          <span className="mb-1 block text-sm text-zinc-400">Password</span>
          <input
            type="password"
            required
            minLength={10}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
          <span className="mt-1 block text-xs text-zinc-600">
            At least 10 characters
          </span>
        </label>

        {setup.isError && (
          <p className="text-sm text-red-400">
            {setup.error instanceof Error ? setup.error.message : "Setup failed"}
          </p>
        )}

        <button
          type="submit"
          disabled={setup.isPending}
          className="w-full rounded-md bg-zinc-100 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {setup.isPending ? "Creating…" : "Create admin account"}
        </button>
      </form>
    </div>
  );
}
