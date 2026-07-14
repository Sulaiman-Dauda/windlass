import { Component, type ErrorInfo, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";

interface BoundaryProps {
  children: ReactNode;
  resetKey: string;
}

interface BoundaryState {
  error: Error | null;
}

class Boundary extends Component<BoundaryProps, BoundaryState> {
  state: BoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): BoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Route render failed", error, info.componentStack);
  }

  componentDidUpdate(previous: BoundaryProps) {
    if (previous.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="rounded-[13px] border border-hairline bg-err-soft p-5">
        <h1 className="text-lg font-semibold text-err">This page could not be displayed</h1>
        <p className="mt-2 text-sm text-fg2">{this.state.error.message}</p>
        <div className="mt-4 flex gap-2">
          <button
            onClick={() => window.location.reload()}
            className="rounded-[10px] bg-accent-fill px-4 py-2.5 text-md font-semibold text-onaccent hover:bg-accent-fill-hi"
          >
            Reload page
          </button>
          <Link
            to="/projects"
            className="rounded-[10px] border border-edge bg-surface px-4 py-2.5 text-md font-semibold text-fg no-underline hover:bg-surface2"
          >
            Back to projects
          </Link>
        </div>
      </div>
    );
  }
}

export default function RouteErrorBoundary({ children }: { children: ReactNode }) {
  const location = useLocation();
  return <Boundary resetKey={location.pathname}>{children}</Boundary>;
}
