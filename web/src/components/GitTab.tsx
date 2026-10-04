import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useCan } from "../api/auth";
import { useProject } from "../api/projects";
import { Button } from "../ui/Button";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { Field, Input, Select, Switch } from "../ui/Field";
import { Callout, FormError } from "../ui/Page";
import { CopyField } from "../ui/Copy";
import { useToast } from "../ui/Toast";

interface Connection {
  id: number;
  provider: string;
  name: string;
}

interface Repo {
  full_name: string;
  clone_url: string;
  default_branch: string;
  private: boolean;
}

interface SaveResult {
  webhook_secret: string;
  webhook_registered: boolean;
}

const CUSTOM = "__custom";

export default function GitTab({ project }: { project: string }) {
  const qc = useQueryClient();
  const proj = useProject(project);
  const can = useCan();
  const toast = useToast();
  const editable = can("member");
  const connections = useQuery<Connection[]>({
    queryKey: ["git", "connections"],
    queryFn: () => api("/git/connections"),
  });

  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [connectionId, setConnectionId] = useState(0);
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [customUrl, setCustomUrl] = useState(false);
  const [result, setResult] = useState<SaveResult | null>(null);
  const [initialized, setInitialized] = useState(false);

  if (proj.data && !initialized) {
    setRepo(proj.data.git_repo ?? "");
    setBranch(proj.data.git_branch ?? "main");
    setAutoDeploy(proj.data.auto_deploy);
    setInitialized(true);
  }

  const repos = useQuery<Repo[]>({
    queryKey: ["git", "repos", connectionId],
    queryFn: () => api(`/git/connections/${connectionId}/repos`),
    enabled: connectionId > 0,
    staleTime: 60 * 1000,
    retry: false,
  });

  const save = useMutation({
    mutationFn: () =>
      api<SaveResult>(`/projects/${project}/git`, {
        method: "PUT",
        body: JSON.stringify({
          repo,
          branch,
          auto_deploy: autoDeploy,
          connection_id: connectionId || undefined,
        }),
      }),
    onSuccess: (data) => {
      setResult(data);
      toast.ok("Git settings saved");
      qc.invalidateQueries({ queryKey: ["projects", project] });
    },
  });

  const provider = connections.data?.find((c) => c.id === connectionId)?.provider ?? "github";
  const webhookUrl = `${window.location.origin}/api/v1/webhooks/${provider}/${project}`;

  // The picker shows once a connection's repos load; a repo URL that is not
  // in the list (or the explicit choice) falls back to the manual URL input.
  const showPicker = connectionId > 0 && (repos.data?.length ?? 0) > 0;
  const matched = repos.data?.some((r) => r.clone_url === repo) ?? false;
  const pickerValue = customUrl || (repo !== "" && !matched) ? CUSTOM : repo;
  const showUrlInput = !showPicker || pickerValue === CUSTOM;

  return (
    <div className="max-w-3xl space-y-5">
      <Card>
        <CardHeader
          title="Repository"
          description="Windlass pulls the branch, then runs compose pull, build and up. With auto-deploy on, every push to the branch deploys."
        />
        <form
          id="git-settings"
          className="space-y-4 px-5 py-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field
            label="Connection"
            hint={
              <>
                Needed for private repositories. Add one in{" "}
                <Link to="/settings/git" className="text-accent hover:underline">
                  Settings, Git
                </Link>
                .
              </>
            }
          >
            <Select
              value={connectionId}
              disabled={!editable}
              onChange={(e) => setConnectionId(parseInt(e.target.value, 10))}
            >
              <option value={0}>None (public repository)</option>
              {connections.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.provider})
                </option>
              ))}
            </Select>
          </Field>

          {showPicker && (
            <Field label="Repository">
              <Select
                value={pickerValue}
                disabled={!editable}
                onChange={(e) => {
                  const v = e.target.value;
                  if (v === CUSTOM) {
                    setCustomUrl(true);
                    return;
                  }
                  setCustomUrl(false);
                  setRepo(v);
                  const picked = repos.data?.find((r) => r.clone_url === v);
                  if (picked?.default_branch) setBranch(picked.default_branch);
                }}
              >
                <option value="">Choose a repository…</option>
                {repos.data?.map((r) => (
                  <option key={r.clone_url} value={r.clone_url}>
                    {r.full_name}
                    {r.private ? " (private)" : ""}
                  </option>
                ))}
                <option value={CUSTOM}>Enter a URL instead…</option>
              </Select>
            </Field>
          )}
          {connectionId > 0 && repos.isError && (
            <p className="text-xs text-fg3">Could not list repositories for this connection. Enter the clone URL below.</p>
          )}

          {showUrlInput && (
            <Field label="Clone URL (https)">
              <Input
                required
                readOnly={!editable}
                value={repo}
                onChange={(e) => setRepo(e.target.value)}
                placeholder="https://github.com/acme/app.git"
                spellCheck={false}
                className="font-mono"
              />
            </Field>
          )}

          <Field label="Branch" className="max-w-xs">
            <Input
              required
              readOnly={!editable}
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              spellCheck={false}
              className="font-mono"
            />
          </Field>

          <Switch
            checked={autoDeploy}
            onChange={setAutoDeploy}
            disabled={!editable}
            label="Deploy automatically on push"
            description="Uses a webhook on the repository. Turn off to deploy only when you press Deploy."
          />
          <FormError error={save.error} fallback="Save failed" />
        </form>
        {editable && (
          <CardFooter note="Saving does not deploy. Press Deploy, or push to the branch.">
            <Button type="submit" form="git-settings" variant="primary" size="sm" disabled={!repo} loading={save.isPending}>
              Save git settings
            </Button>
          </CardFooter>
        )}
      </Card>

      {result?.webhook_registered && (
        <Callout tone="ok" title="Auto-deploy is live">
          The webhook was registered on the repository for you. Every push to{" "}
          <span className="font-mono text-xs">{branch}</span> now deploys. Nothing else to set up.
        </Callout>
      )}

      {result && !result.webhook_registered && (
        <Card>
          <CardHeader
            title="Add this webhook to your repository"
            description="Windlass couldn't register it automatically. Paste these into the repository's webhook settings."
          />
          <div className="space-y-4 px-5 py-4">
            <Field as="div" label={`Payload URL (${provider === "gitlab" ? "GitLab" : "GitHub"})`}>
              <CopyField value={webhookUrl} />
            </Field>
            <Field as="div" label="Secret" hint="Shown once. GitHub calls it the webhook secret, GitLab the secret token.">
              <CopyField value={result.webhook_secret} />
            </Field>
            <Field as="div" label="Content type">
              <CopyField value="application/json" />
            </Field>
          </div>
        </Card>
      )}
    </div>
  );
}
