import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useLogin } from "../api/auth";
import { Button, btn } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { Icon } from "../ui/Icon";
import AuthFrame from "../components/AuthFrame";

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
  const hasOAuth = providers.data?.github || providers.data?.google;

  return (
    <AuthFrame footer="Your apps keep running whether or not this panel is.">
      <h1 className="text-xl font-semibold tracking-[-0.016em] text-fg">Sign in</h1>
      <p className="mt-1 text-sm text-fg3">Sign in to your server</p>

      <form
        className="mt-6 flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate({ email, password, totp_code: totp || undefined });
        }}
      >
        <Field label="Email">
          <Input
            type="email"
            required
            autoFocus
            autoComplete="username"
            controlSize="lg"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>

        <Field label="Password">
          <Input
            type="password"
            required
            autoComplete="current-password"
            controlSize="lg"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>

        {needsTotp && (
          <Field label="Authenticator code" hint="The 6-digit code from your authenticator app.">
            <Input
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]{6}"
              maxLength={6}
              required
              controlSize="lg"
              value={totp}
              onChange={(e) => setTotp(e.target.value.replace(/\D/g, ""))}
              className="text-center font-mono tracking-[0.4em]"
            />
          </Field>
        )}

        {login.isError && login.error instanceof ApiError && login.error.code !== "totp_required" && (
          <p role="alert" className="text-sm text-err">
            {login.error.message}
          </p>
        )}

        <Button type="submit" variant="primary" size="lg" block loading={login.isPending}>
          {login.isPending ? "Signing in…" : "Sign in"}
        </Button>
      </form>

      {hasOAuth && (
        <>
          <div className="my-5 flex items-center gap-3 text-xs text-fg3">
            <span className="h-px flex-1 bg-hairline" />
            or
            <span className="h-px flex-1 bg-hairline" />
          </div>
          <div className="flex flex-col gap-2">
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
        </>
      )}
    </AuthFrame>
  );
}
