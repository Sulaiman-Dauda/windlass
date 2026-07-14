import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { Page } from "../ui/Page";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Input } from "../ui/Field";
import { Icon } from "../ui/Icon";

interface Template {
  key: string;
  name: string;
  description: string;
  default_port: number;
  route?: { service: string; container_port: number };
}

export default function Templates() {
  const templates = useQuery<Template[]>({
    queryKey: ["templates"],
    queryFn: () => api("/templates"),
  });
  const navigate = useNavigate();
  const [selected, setSelected] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [domain, setDomain] = useState("");

  const create = useMutation({
    mutationFn: ({ key, isApp }: { key: string; isApp: boolean }) =>
      api<{ project: { name: string } }>(`/templates/${key}`, {
        method: "POST",
        body: JSON.stringify(isApp ? { name, domain } : { name }),
      }),
    onSuccess: (data) => navigate(`/projects/${data.project.name}/deployments`),
  });

  return (
    <Page title="Templates" subtitle="one-click apps & databases">
      <p className="mb-6 max-w-[68ch] text-sm leading-relaxed text-fg2">
        Each template becomes an ordinary Compose project with generated credentials in its
        Environment tab — no proprietary formats, nothing to lock you in. Apps are served over
        HTTPS on a domain you choose.
      </p>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {templates.data?.map((t) => {
          const isApp = Boolean(t.route);
          return (
            <Card key={t.key} className="flex flex-col gap-3 p-4">
              <div className="flex items-center gap-2.5">
                <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-accent-soft text-accent">
                  <Icon name={isApp ? "globe" : "database"} size={18} />
                </span>
                <span className="text-md font-semibold tracking-[-0.01em]">{t.name}</span>
              </div>
              <p className="flex-1 text-sm leading-relaxed text-fg3">{t.description}</p>
              {selected === t.key ? (
                <form
                  className="flex flex-col gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    create.mutate({ key: t.key, isApp });
                  }}
                >
                  <Input
                    autoFocus
                    required
                    value={name}
                    onChange={(e) => setName(e.target.value.toLowerCase())}
                    placeholder="project name"
                    pattern="[a-z0-9][a-z0-9_-]*"
                  />
                  {isApp && (
                    <Input
                      required
                      value={domain}
                      onChange={(e) => setDomain(e.target.value.toLowerCase())}
                      placeholder="domain (e.g. blog.example.com)"
                      pattern="[a-z0-9.-]+\.[a-z0-9.-]+"
                    />
                  )}
                  <Button type="submit" variant="primary" block disabled={create.isPending}>
                    {create.isPending ? "Creating…" : "Create & deploy"}
                  </Button>
                  {create.isError && (
                    <p className="text-xs text-err">
                      {create.error instanceof Error ? create.error.message : "Failed"}
                    </p>
                  )}
                </form>
              ) : (
                <Button
                  block
                  onClick={() => {
                    setSelected(t.key);
                    setName(t.key);
                    setDomain("");
                  }}
                >
                  Create
                </Button>
              )}
            </Card>
          );
        })}
      </div>
    </Page>
  );
}
