import type { AdoptionConflict, ScanResult } from "../api/projects";
import { Button } from "../ui/Button";
import { Modal } from "../ui/Modal";
import { plural } from "../ui/format";

// The server's message marks commands with backticks; show them as code.
function Message({ text }: { text: string }) {
  return (
    <p className="text-sm text-fg2">
      {text.split("`").map((part, i) =>
        i % 2 ? (
          <code key={i} className="font-mono text-fg">
            {part}
          </code>
        ) : (
          part
        ),
      )}
    </p>
  );
}

function Conflict({ c }: { c: AdoptionConflict }) {
  return (
    <li className="flex flex-col gap-1">
      <div className="text-sm font-semibold text-fg">
        {c.project} <span className="font-normal text-fg2">runs as</span> {c.running_as}
      </div>
      <Message text={c.message} />
    </li>
  );
}

// Shown after a scan that passed over a directory or found a stack running
// under another name, so neither goes unnoticed.
export function ScanReport({ result, onClose }: { result: ScanResult; onClose: () => void }) {
  return (
    <Modal
      onClose={onClose}
      width={560}
      title="Scan complete"
      description={`${plural(result.count, "project")} found in the stacks directory. Some need a look before they deploy.`}
      footer={
        <Button variant="primary" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div className="flex flex-col gap-5">
        {result.conflicts.length > 0 && (
          <section className="flex flex-col gap-2">
            <h3 className="text-sm font-semibold text-fg">Deploy blocked until fixed</h3>
            <ul className="flex flex-col gap-3">
              {result.conflicts.map((c) => (
                <Conflict key={`${c.project}/${c.running_as}/${c.working_dir}`} c={c} />
              ))}
            </ul>
          </section>
        )}
        {result.skipped.length > 0 && (
          <section className="flex flex-col gap-2">
            <h3 className="text-sm font-semibold text-fg">Not picked up</h3>
            <ul className="flex flex-col gap-2">
              {result.skipped.map((s) => (
                <li key={s.name} className="text-sm text-fg2">
                  <span className="font-mono text-fg">{s.name}</span>: {s.reason}
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </Modal>
  );
}

// Shown when Deploy is refused because the first deploy would start a
// second copy of a running stack with empty volumes.
export function AdoptionBlocked({ message, onClose }: { message: string; onClose: () => void }) {
  return (
    <Modal
      onClose={onClose}
      width={520}
      title="Deploy not started"
      description="A stack from this directory is already running under another name."
      footer={
        <Button variant="primary" onClick={onClose}>
          Close
        </Button>
      }
    >
      <Message text={message} />
    </Modal>
  );
}
