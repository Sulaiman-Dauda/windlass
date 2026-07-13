import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useLogin } from "../api/auth";

export default function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const login = useLogin();

  const providers = useQuery<Record<string, boolean>>({
    queryKey: ["auth", "oauth-providers"],
    queryFn: () => api("/auth/oauth/providers"),
  });

  const needsTotp =
    login.error instanceof ApiError &&
    (login.error.code === "totp_required" || login.error.code === "totp_invalid");

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <form
        className="w-full max-w-sm space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate({ email, password, totp_code: totp || undefined });
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

        {needsTotp && (
          <label className="block">
            <span className="mb-1 block text-sm text-zinc-400">
              Authenticator code
            </span>
            <input
              autoFocus
              inputMode="numeric"
              pattern="[0-9]{6}"
              maxLength={6}
              required
              value={totp}
              onChange={(e) => setTotp(e.target.value.replace(/\D/g, ""))}
              className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-2 text-center font-mono text-lg tracking-widest text-zinc-100 outline-none focus:border-zinc-600"
            />
          </label>
        )}

        {login.isError && login.error instanceof ApiError && login.error.code !== "totp_required" && (
          <p className="text-sm text-red-400">{login.error.message}</p>
        )}

        <button
          type="submit"
          disabled={login.isPending}
          className="w-full rounded-md bg-zinc-100 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>

        {(providers.data?.github || providers.data?.google) && (
          <div className="space-y-2 border-t border-zinc-900 pt-4">
            {providers.data?.github && (
              <a
                href="/api/v1/auth/oauth/github/start"
                className="block w-full rounded-md border border-zinc-700 py-2 text-center text-sm text-zinc-200 hover:bg-zinc-900"
              >
                Continue with GitHub
              </a>
            )}
            {providers.data?.google && (
              <a
                href="/api/v1/auth/oauth/google/start"
                className="block w-full rounded-md border border-zinc-700 py-2 text-center text-sm text-zinc-200 hover:bg-zinc-900"
              >
                Continue with Google
              </a>
            )}
          </div>
        )}
      </form>
    </div>
  );
}
