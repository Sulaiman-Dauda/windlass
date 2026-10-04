import { useState } from "react";
import { useSetup } from "../api/auth";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { FormError } from "../ui/Page";
import AuthFrame from "../components/AuthFrame";

// First-run flow: the server prints a one-time setup token to its log; the
// admin pastes it here to claim the instance.
export default function Setup() {
  const [token, setToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const setup = useSetup();

  return (
    <AuthFrame
      footer={
        <>
          Lost the token? It's in the service log:{" "}
          <code className="rounded-[5px] bg-sunken px-1.5 py-0.5 font-mono text-fg2">journalctl -u windlass | grep setup_token</code>
        </>
      }
    >
      <h1 className="text-xl font-semibold tracking-[-0.016em] text-fg">Welcome to Windlass</h1>
      <p className="mt-1 text-sm text-fg3">
        Claim this server by creating its first admin account. The setup token is printed in the server log at
        first start.
      </p>

      <form
        className="mt-6 flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setup.mutate({ token, email, password });
        }}
      >
        <Field label="Setup token">
          <Input
            required
            autoFocus
            spellCheck={false}
            autoComplete="off"
            controlSize="lg"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            className="font-mono"
          />
        </Field>
        <Field label="Email">
          <Input
            type="email"
            required
            autoComplete="username"
            controlSize="lg"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field label="Password" hint="At least 10 characters.">
          <Input
            type="password"
            required
            minLength={10}
            autoComplete="new-password"
            controlSize="lg"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>

        <FormError error={setup.error} fallback="Setup failed" />

        <Button type="submit" variant="primary" size="lg" block loading={setup.isPending}>
          {setup.isPending ? "Creating…" : "Create admin account"}
        </Button>
      </form>
    </AuthFrame>
  );
}
