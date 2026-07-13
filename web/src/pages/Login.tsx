import { useState } from "react";
import { useLogin } from "../api/auth";

export default function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const login = useLogin();

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <form
        className="w-full max-w-sm space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate({ email, password });
        }}
      >
        <div className="text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-100">
            Windlass
          </h1>
          <p className="mt-1 text-sm text-zinc-500">Sign in to your server</p>
        </div>

        <label className="block">
          <span className="mb-1 block text-sm text-zinc-400">Email</span>
          <input
            type="email"
            required
            autoComplete="username"
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
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>

        {login.isError && (
          <p className="text-sm text-red-400">
            {login.error instanceof Error ? login.error.message : "Login failed"}
          </p>
        )}

        <button
          type="submit"
          disabled={login.isPending}
          className="w-full rounded-md bg-zinc-100 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
