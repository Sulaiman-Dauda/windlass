import { useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { Card, CardHeader } from "../ui/Card";
import { Button, btn } from "../ui/Button";
import { Input, Select, Field } from "../ui/Field";
import { FormError } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { formatDateTime, timeAgo } from "../ui/format";

export interface AuditEntry {
  id: number;
  ts: string;
  user_id: number | null;
  user_email: string;
  token_id: number | null;
  token_name: string;
  action: string;
  resource_type: string;
  resource_id: string;
  ip: string;
  detail: unknown;
}

interface AuditPage {
  entries: AuditEntry[];
  next_before_id: number | null;
}

export interface AuditFilter {
  action: string;
  user_id: string;
  since: string;
  until: string;
}

const empty: AuditFilter = { action: "", user_id: "", since: "", until: "" };

/** The query string the list and the CSV export share. Empty values are left out. */
export function auditQuery(f: AuditFilter, extra: Record<string, string> = {}): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries({ ...f, ...extra })) if (v) q.set(k, v);
  return q.toString();
}

/** Each action's family ("project.deploy" gives "project.*"), so one choice covers them all. */
export function actionOptions(actions: string[]): { value: string; label: string }[] {
  const families = [...new Set(actions.filter((a) => a.includes(".")).map((a) => a.slice(0, a.indexOf(".") + 1)))];
  return [
    ...families.map((f) => ({ value: `${f}*`, label: `${f}* (all)` })),
    ...actions.map((a) => ({ value: a, label: a })),
  ].sort((x, y) => x.value.localeCompare(y.value));
}

function who(e: AuditEntry): string {
  const user = e.user_email || (e.user_id ? `User #${e.user_id} (removed)` : "No user");
  if (!e.token_id) return user;
  return `${user}, token ${e.token_name || `#${e.token_id} (revoked)`}`;
}

export function AuditLogSection() {
  const [filter, setFilter] = useState<AuditFilter>(empty);
  const actions = useQuery<{ actions: string[] }>({
    queryKey: ["system", "audit", "actions"],
    queryFn: () => api("/system/audit/actions"),
  });
  const users = useQuery<{ id: number; email: string }[]>({ queryKey: ["users"], queryFn: () => api("/users") });
  const log = useInfiniteQuery<AuditPage>({
    queryKey: ["system", "audit", filter],
    queryFn: ({ pageParam }) =>
      api(`/system/audit?${auditQuery(filter, pageParam ? { before_id: String(pageParam) } : {})}`),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_before_id ?? undefined,
  });
  const entries = log.data?.pages.flatMap((p) => p.entries) ?? [];
  const set = (k: keyof AuditFilter) => (e: { target: { value: string } }) => setFilter({ ...filter, [k]: e.target.value });

  return (
    <Card>
      <CardHeader
        title="Audit log"
        description="Every change made in the panel or through an API token, newest first. Times are in your browser's time zone; the date filters use UTC days."
        actions={
          <a className={btn("secondary", "sm")} href={`/api/v1/system/audit/export?${auditQuery(filter)}`} download>
            <Icon name="download" size={14} />
            Export CSV
          </a>
        }
      />
      <div className="grid grid-cols-1 gap-3 border-b border-hairline px-5 py-4 sm:grid-cols-2 lg:grid-cols-4">
        <Field label="Action">
          <Select value={filter.action} onChange={set("action")}>
            <option value="">All actions</option>
            {actionOptions(actions.data?.actions ?? []).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="User">
          <Select value={filter.user_id} onChange={set("user_id")}>
            <option value="">All users</option>
            {(users.data ?? []).map((u) => (
              <option key={u.id} value={String(u.id)}>
                {u.email}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="From">
          <Input type="date" value={filter.since} onChange={set("since")} />
        </Field>
        <Field label="To">
          <Input type="date" value={filter.until} onChange={set("until")} />
        </Field>
      </div>
      {log.isLoading ? (
        <div className="p-5">
          <Skeleton className="h-5 w-1/2" />
        </div>
      ) : log.error ? (
        <div className="p-5">
          <FormError error={log.error} fallback="Could not load the audit log" />
        </div>
      ) : entries.length === 0 ? (
        <p className="px-5 py-6 text-sm text-fg3">No entries match.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-sm">
            <thead>
              <tr className="border-b border-hairline bg-surface2 text-left text-xs text-fg3">
                <th className="h-9 px-5 font-medium">Time</th>
                <th className="h-9 px-4 font-medium">Action</th>
                <th className="h-9 px-4 font-medium">On</th>
                <th className="h-9 px-4 font-medium">By</th>
                <th className="h-9 px-4 font-medium">IP</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {entries.map((e) => (
                <tr key={e.id} className="align-top">
                  <td className="whitespace-nowrap px-5 py-3">
                    <div className="text-fg">{formatDateTime(e.ts)}</div>
                    <div className="text-xs text-fg3">{timeAgo(e.ts)}</div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="font-mono text-xs text-fg">{e.action}</div>
                    {e.detail != null && (
                      <div className="mt-1 max-w-80 break-all font-mono text-xs text-fg3">{JSON.stringify(e.detail)}</div>
                    )}
                  </td>
                  <td className="px-4 py-3 text-fg2">
                    {e.resource_type ? `${e.resource_type} ${e.resource_id}`.trim() : ""}
                  </td>
                  <td className="px-4 py-3 text-fg2">{who(e)}</td>
                  <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-fg3">{e.ip}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {log.hasNextPage && (
        <div className="border-t border-hairline px-5 py-3">
          <Button variant="secondary" size="sm" loading={log.isFetchingNextPage} onClick={() => log.fetchNextPage()}>
            Load older entries
          </Button>
        </div>
      )}
    </Card>
  );
}
