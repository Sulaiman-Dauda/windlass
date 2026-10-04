import { createContext, useContext } from "react";

/** What the app shell lets a page reach: the mobile drawer and the command palette. */
export interface Shell {
  openNav: () => void;
  openSearch: () => void;
}

export const ShellContext = createContext<Shell>({ openNav: () => {}, openSearch: () => {} });

export function useShell(): Shell {
  return useContext(ShellContext);
}
