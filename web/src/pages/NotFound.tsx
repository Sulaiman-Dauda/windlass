import { Link } from "react-router";
import { Page, EmptyState } from "../ui/Page";
import { btn } from "../ui/Button";

export default function NotFound() {
  return (
    <Page title="Not found">
      <EmptyState
        icon="search"
        title="There's nothing at this address"
        desc="The page may have moved, or the project it belonged to was deleted."
        actions={
          <>
            <Link to="/" className={btn("secondary", "md")}>
              Go to overview
            </Link>
            <Link to="/projects" className={btn("primary", "md")}>
              View projects
            </Link>
          </>
        }
      />
    </Page>
  );
}
