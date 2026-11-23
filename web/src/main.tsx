import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import App from "./App";
import { ThemeProvider } from "./ui/theme";
import { ToastProvider } from "./ui/Toast";
import { ConfirmProvider } from "./ui/Modal";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

// The panel's four faces are embedded in index.css, and Chrome decodes a face
// only when text first uses it, so the first page with mono text painted it
// in the fallback and then reflowed by a pixel. Decoding them all before the
// first render takes a few milliseconds; the timeout means a face that never
// loads cannot hold the panel back.
const fontsReady = Promise.race([
  Promise.all([...document.fonts].map((f) => f.load())),
  new Promise((resolve) => setTimeout(resolve, 500)),
]).catch(() => undefined);

fontsReady.then(() => createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <ToastProvider>
            <ConfirmProvider>
              <App />
            </ConfirmProvider>
          </ToastProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
));
