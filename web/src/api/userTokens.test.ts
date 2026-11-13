import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./client";
import { deleteUserToken, userTokensKey } from "./userTokens";

vi.mock("./client", () => ({ api: vi.fn() }));

describe("deleteUserToken", () => {
  beforeEach(() => {
    vi.mocked(api).mockReset();
    vi.mocked(api).mockResolvedValue(undefined);
  });

  it("deletes that user's token by id, with the DELETE method", async () => {
    await deleteUserToken(new QueryClient(), 7, 3);
    expect(api).toHaveBeenCalledTimes(1);
    expect(api).toHaveBeenCalledWith("/users/7/tokens/3", { method: "DELETE" });
  });

  it("refreshes the open user's token list and the signed-in user's own tokens", async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    await deleteUserToken(qc, 7, 3);
    expect(spy).toHaveBeenCalledWith({ queryKey: userTokensKey(7) });
    expect(spy).toHaveBeenCalledWith({ queryKey: ["tokens"] });
  });

  it("does not refresh anything when the revoke fails", async () => {
    vi.mocked(api).mockRejectedValue(new Error("forbidden"));
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    await expect(deleteUserToken(qc, 7, 3)).rejects.toThrow("forbidden");
    expect(spy).not.toHaveBeenCalled();
  });
});

describe("userTokensKey", () => {
  it("keys each user's list separately", () => {
    expect(userTokensKey(7)).toEqual(["user-tokens", 7]);
    expect(userTokensKey(8)).not.toEqual(userTokensKey(7));
  });
});
