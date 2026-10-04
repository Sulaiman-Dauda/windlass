import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useCan } from "../api/auth";
import { Page, FormError } from "../ui/Page";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { Tag } from "../ui/Badge";
import { Segmented } from "../ui/Segmented";
import { Skeleton } from "../ui/Skeleton";
import { Modal } from "../ui/Modal";
import { useToast } from "../ui/Toast";
import { Icon } from "../ui/Icon";

interface Template {
  key: string;
  name: string;
  description: string;
  default_port: number;
  route?: { service: string; container_port: number };
}

type Kind = "all" | "app" | "service";

export default function Templates() {
  const templates = useQuery<Template[]>({
    queryKey: ["templates"],
    queryFn: () => api("/templates"),
  });
  const can = useCan();
  const [kind, setKind] = useState<Kind>("all");
  const [chosen, setChosen] = useState<Template | null>(null);

  const list = (templates.data ?? []).filter((t) => kind === "all" || (kind === "app") === Boolean(t.route));

  return (
    <Page
      title="Templates"
      description="One-click apps and services. Each becomes an ordinary Compose project with generated credentials in its Environment tab: no proprietary format, nothing to lock you in."
    >
      {chosen && <CreateFromTemplate template={chosen} onClose={() => setChosen(null)} />}

      <Segmented
        className="mb-5"
        label="Show"
        value={kind}
        onChange={setKind}
        options={[
          { value: "all", label: "All" },
          { value: "app", label: "Web apps" },
          { value: "service", label: "Services" },
        ]}
      />

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
        {templates.isLoading && [0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-[196px] rounded-card" />)}
        {list.map((t) => {
          const isApp = Boolean(t.route);
          return (
            <Card key={t.key} className="flex flex-col p-5 transition-[border-color,box-shadow] duration-150 hover:border-edge hover:shadow-[var(--shadow-md)]">
              <div className="flex items-start justify-between gap-3">
                <span className="grid h-10 w-10 place-items-center rounded-[10px] border border-hairline bg-surface2 text-fg2">
                  <Icon name={isApp ? "globe" : "database"} size={19} />
                </span>
                <Tag tone={isApp ? "accent" : "idle"}>{isApp ? "Web app" : "Service"}</Tag>
              </div>
              <h3 className="mt-4 text-md font-semibold tracking-[-0.01em] text-fg">{t.name}</h3>
              <p className="mt-1 line-clamp-3 flex-1 text-sm text-fg3">{t.description}</p>
              <div className="mt-4 flex items-center justify-between gap-3 border-t border-hairline pt-3.5">
                <span className="font-mono text-xs text-fg3">
                  {isApp ? `HTTPS to :${t.route!.container_port}` : `port ${t.default_port}`}
                </span>
                {can("member") && (
                  <Button size="sm" onClick={() => setChosen(t)}>
                    Create
                  </Button>
                )}
              </div>
            </Card>
          );
        })}
      </div>
    </Page>
  );
}

function CreateFromTemplate({ template, onClose }: { template: Template; onClose: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const isApp = Boolean(template.route);
  const [name, setName] = useState(template.key);
  const [domain, setDomain] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api<{ project: { name: string } }>(`/templates/${template.key}`, {
        method: "POST",
        body: JSON.stringify(isApp ? { name, domain } : { name }),
      }),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["projects"], exact: true });
      toast.info(`Creating ${data.project.name}`, "The first deployment is running.");
      navigate(`/projects/${data.project.name}/deployments`);
    },
  });

  return (
    <Modal
      onClose={onClose}
      title={`Create ${template.name}`}
      description={template.description}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="create-template" variant="primary" loading={create.isPending}>
            Create and deploy
          </Button>
        </>
      }
    >
      <form
        id="create-template"
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Project name" hint="Lowercase letters, digits, - and _.">
          <Input
            autoFocus
            required
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            placeholder="project-name"
            pattern="[a-z0-9][a-z0-9_-]*"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        {isApp && (
          <Field label="Domain" hint="Point its DNS at this server. HTTPS is set up automatically.">
            <Input
              required
              value={domain}
              onChange={(e) => setDomain(e.target.value.toLowerCase().trim())}
              placeholder="blog.example.com"
              pattern="[a-z0-9.-]+\.[a-z0-9.-]+"
              spellCheck={false}
            />
          </Field>
        )}
        <FormError error={create.error} fallback="Could not create the project" />
      </form>
    </Modal>
  );
}
