import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useDeleteProject } from "../api/projects";
import { Modal } from "../ui/Modal";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { Spinner } from "../ui/Spinner";

interface Me {
  has_password: boolean;
}

export default function DeleteProjectDialog({
  name,
  onClose,
  onDeleted,
}: {
  name: string;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const me = useQuery<Me>({ queryKey: ["auth", "me"], queryFn: () => api("/auth/me") });
  const del = useDeleteProject();
  const [typed, setTyped] = useState("");
  const [password, setPassword] = useState("");

  const needsPassword = me.data?.has_password ?? true;
  const ready = typed === name && (!needsPassword || password.length > 0);

  return (
    <Modal onClose={onClose} labelledBy="del-title">
      <h3 id="del-title" className="text-lg font-semibold tracking-[-0.01em] text-err">
        Delete {name}
      </h3>
      <p className="mt-2 text-sm leading-relaxed text-fg2">
        This stops the project's containers and permanently removes its directory, including
        compose files and environment values. This can't be undone.
      </p>

      <form
        className="mt-5 flex flex-col gap-3.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (!ready || del.isPending) return;
          del.mutate(
            { name, password: needsPassword ? password : undefined },
            { onSuccess: onDeleted },
          );
        }}
      >
        <Field
          label={
            <>
              Type <span className="font-mono text-fg">{name}</span> to confirm
            </>
          }
        >
          <Input
            autoFocus
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={name}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
          />
        </Field>

        {needsPassword && (
          <Field label="Your password">
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </Field>
        )}

        {del.isError && (
          <p className="text-sm text-err">
            {del.error instanceof Error ? del.error.message : "Delete failed"}
          </p>
        )}

        <div className="mt-1 flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="dangerSolid" disabled={!ready || del.isPending}>
            {del.isPending ? <><Spinner /> Deleting…</> : "Delete project"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
