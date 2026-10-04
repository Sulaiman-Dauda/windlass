import { Component, type ErrorInfo, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { Button, btn } from "../ui/Button";
import { Icon } from "../ui/Icon";

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
      <div className="grid min-h-[60vh] place-items-center px-4">
        <div className="max-w-md text-center">
          <div className="mx-auto grid h-11 w-11 place-items-center rounded-[10px] border border-hairline bg-err-soft text-err">
            <Icon name="warning" size={20} />
          </div>
          <h1 className="mt-4 text-lg font-semibold text-fg">This page could not be displayed</h1>
          <p className="mt-1 break-words text-sm text-fg3">{this.state.error.message}</p>
          <div className="mt-5 flex justify-center gap-2">
            <Link to="/projects" className={btn("secondary", "md")}>
              Back to projects
            </Link>
            <Button variant="primary" icon="refresh" onClick={() => window.location.reload()}>
              Reload page
            </Button>
          </div>
        </div>
      </div>
    );
  }
}

export default function RouteErrorBoundary({ children }: { children: ReactNode }) {
  const location = useLocation();
  return <Boundary resetKey={location.pathname}>{children}</Boundary>;
}
