import type { QueryClient } from "@tanstack/react-query";
import { api } from "./client";

/** Query key for one user's tokens, as an admin sees them in the Users card. */
export const userTokensKey = (userId: number | null) => ["user-tokens", userId] as const;

/**
 * Revokes one of a user's tokens (admin only) and refreshes both the open
 * list in the Users card and the signed-in user's own token list, in case the
 * admin revoked their own token.
 */
export async function deleteUserToken(qc: QueryClient, userId: number, id: number) {
  await api(`/users/${userId}/tokens/${id}`, { method: "DELETE" });
  await qc.invalidateQueries({ queryKey: userTokensKey(userId) });
  await qc.invalidateQueries({ queryKey: ["tokens"] });
}
