import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useLogin } from "../api/auth";
import { Logo } from "../ui/Logo";
import { Button, btn } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { Icon } from "../ui/Icon";
import { Spinner } from "../ui/Spinner";

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
    <div className="grid min-h-screen place-items-center bg-canvas px-5">
      <form
        className="w-full max-w-[384px]"
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate({ email, password, totp_code: totp || undefined });
        }}
      >
        <div className="mb-7 flex flex-col items-center text-center">
          <Logo size={46} />
          <h1 className="mt-3.5 text-2xl font-bold tracking-[-0.02em]">Windlass</h1>
          <p className="mt-1 text-sm text-fg3">Sign in to your server</p>
        </div>

        <div className="flex flex-col gap-4">
          <Field label="Email">
            <Input
              type="email"
              required
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </Field>

          <Field label="Password">
            <Input
              type="password"
              required
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>

          {needsTotp && (
            <Field label="Authenticator code">
              <Input
                autoFocus
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                value={totp}
                onChange={(e) => setTotp(e.target.value.replace(/\D/g, ""))}
                className="text-center font-mono text-lg tracking-[0.4em]"
              />
            </Field>
          )}

          {login.isError && login.error instanceof ApiError && login.error.code !== "totp_required" && (
            <p className="text-sm text-err">{login.error.message}</p>
          )}

          <Button type="submit" variant="primary" size="lg" block disabled={login.isPending}>
            {login.isPending ? <><Spinner /> Signing in…</> : "Sign in"}
          </Button>

          {(providers.data?.github || providers.data?.google) && (
            <div className="mt-1 flex flex-col gap-2 border-t border-hairline pt-4">
              {providers.data?.github && (
                <a href="/api/v1/auth/oauth/github/start" className={btn("secondary", "lg", "w-full")}>
                  <Icon name="github" size={17} /> Continue with GitHub
                </a>
              )}
              {providers.data?.google && (
                <a href="/api/v1/auth/oauth/google/start" className={btn("secondary", "lg", "w-full")}>
                  Continue with Google
                </a>
              )}
            </div>
          )}
        </div>
      </form>
    </div>
  );
}
