import { useState } from "react";
import { useSetup } from "../api/auth";
import { Logo } from "../ui/Logo";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { Spinner } from "../ui/Spinner";

// First-run flow: the server prints a one-time setup token to its log; the
// admin pastes it here to claim the instance.
export default function Setup() {
  const [token, setToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const setup = useSetup();

  return (
    <div className="grid min-h-screen place-items-center bg-canvas px-5">
      <form
        className="w-full max-w-[400px]"
        onSubmit={(e) => {
          e.preventDefault();
          setup.mutate({ token, email, password });
        }}
      >
        <div className="mb-7 flex flex-col items-center text-center">
          <Logo size={46} className="text-accent" />
          <h1 className="mt-3.5 text-2xl font-bold tracking-[-0.02em]">Welcome to Windlass</h1>
          <p className="mt-1 max-w-[320px] text-sm text-fg3">
            Create the admin account. Your setup token is printed in the server log at first start.
          </p>
        </div>

        <div className="flex flex-col gap-4">
          <Field label="Setup token">
            <Input required value={token} onChange={(e) => setToken(e.target.value)} className="font-mono" />
          </Field>
          <Field label="Email">
            <Input type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field label="Password" hint="At least 10 characters">
            <Input
              type="password"
              required
              minLength={10}
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>

          {setup.isError && (
            <p className="text-sm text-err">
              {setup.error instanceof Error ? setup.error.message : "Setup failed"}
            </p>
          )}

          <Button type="submit" variant="primary" size="lg" block disabled={setup.isPending}>
            {setup.isPending ? <><Spinner /> Creating…</> : "Create admin account"}
          </Button>
        </div>
      </form>
    </div>
  );
}
