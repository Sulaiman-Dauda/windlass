import { Link } from "react-router";
import { useAttention, type AttentionItem } from "../api/system";
import { Card } from "../ui/Card";
import { Icon, type IconName } from "../ui/Icon";
import { btn } from "../ui/Button";
import { cn } from "../ui/cn";
import { formatStamp, timeAgo } from "../ui/format";

const kindIcon: Record<string, IconName> = {
  deploy_failed: "xCircle",
  disk: "disk",
  no_backup: "database",
  proxy: "warning",
  update: "download",
};

const toneClass: Record<AttentionItem["tone"], string> = {
  error: "bg-err-soft text-err",
  warn: "bg-warn-soft text-warn",
  info: "bg-sunken text-fg2",
};

/**
 * What needs a person on this server, from /system/attention. The card is
 * absent when the list is empty or cannot be read: an empty card would be
 * noise, and the dashboard below still shows the state.
 */
export default function NeedsYou() {
  const attention = useAttention();
  const items = attention.data?.items ?? [];
  if (items.length === 0) return null;
  const checked = attention.data?.checked_at;

  return (
    <Card aria-labelledby="needs-you-title">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-[18px] py-3.5">
        <h2 id="needs-you-title" className="text-md font-semibold tracking-[-0.01em] text-fg">
          Needs you
        </h2>
        <span className="rounded-full bg-sunken px-[7px] text-xs font-semibold tabular-nums text-fg2">{items.length}</span>
        {checked && (
          <p className="hidden text-xs text-fg3 sm:block" title={formatStamp(checked)}>
            Checked {timeAgo(checked)}
          </p>
        )}
      </div>
      <ul>
        {items.map((item, i) => (
          <li
            key={`${item.kind}-${item.project ?? ""}-${i}`}
            className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-3 border-t border-hairline px-4 py-3 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:px-[18px]"
          >
            <span className={cn("grid h-7 w-7 place-items-center self-start rounded-sm sm:self-center", toneClass[item.tone] ?? toneClass.info)}>
              <Icon name={kindIcon[item.kind] ?? "warning"} size={15} />
            </span>
            <div className="grid min-w-0 gap-px">
              <span className="text-sm font-semibold text-fg">{item.title}</span>
              {item.detail && <span className="text-xs text-fg3">{item.detail}</span>}
            </div>
            {item.link && (
              <Link to={item.link} className={btn("secondary", "sm", "col-start-2 justify-self-start sm:col-start-3")}>
                {item.action || "Open"}
              </Link>
            )}
          </li>
        ))}
      </ul>
    </Card>
  );
}
