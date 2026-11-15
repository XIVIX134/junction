import { useEffect, type ReactNode } from "react";
import { Shell, type Page } from "./components/Shell";
import type { Tab } from "./components/ClientPanel";
import { LiveProvider } from "./live";
import { Link, navigate, useLocation } from "./router";
import { Activity } from "./screens/Activity";
import { Clients } from "./screens/Clients";
import { Dashboard } from "./screens/Dashboard";
import { Internet } from "./screens/Internet";
import { Rules } from "./screens/Rules";
import { Topology } from "./screens/Topology";
import { Health } from "./screens/Health";
import { Settings } from "./screens/Settings";
import { Setup } from "./screens/Setup";
import { Login } from "./screens/Login";
import { api2 } from "./api/client";
import { useFetch } from "./live";
import { useBrand } from "./prefs";

const TABS: Tab[] = ["overview", "insights", "settings"];
const TITLE: Record<Page, string> = {
  dashboard: "Dashboard",
  topology: "Topology",
  clients: "Clients",
  rules: "Rules",
  activity: "Activity",
  internet: "Internet",
  health: "Health",
  settings: "Settings",
  none: "Not found",
};

export function App() {
  // Sign-in is optional (Settings → Access). The internet never depends on it.
  const session = useFetch("session", api2.session);
  const s = session.data;
  if (s?.required && !s.signedIn) return <Login onIn={session.setData} />;
  return (
    <LiveProvider>
      <Routes />
    </LiveProvider>
  );
}

/** Old addresses (earlier versions of the app) → their new homes. */
function redirectFor(path: string, search: string): string | null {
  let m: RegExpExecArray | null;
  if (path === "/devices") return "/clients";
  if ((m = /^\/devices\/([^/]+)$/.exec(path))) return `/clients/${m[1]}/settings`;
  if (path === "/exits" || path === "/usage") return "/internet" + (search.includes("bundle") ? "#renew" : "");
  if (path === "/events") return `/activity${search}`;
  if (path === "/policies") return "/rules";
  if ((m = /^\/rules\/(\d+|new)\/graph$/.exec(path))) return `/rules/${m[1]}?view=flow`;
  return null;
}

function Routes() {
  const { path, query } = useLocation();
  const brand = useBrand();
  // First run: until setup is done (or skipped), the dashboard sends you there once.
  useEffect(() => {
    if (path !== "/" || sessionStorage.getItem("setup-seen")) return;
    sessionStorage.setItem("setup-seen", "1");
    api2.setup().then((i) => i.needed && navigate("/setup", { replace: true })).catch(() => {});
  }, [path]);
  const redirect = redirectFor(path, query.toString() ? `?${query}` : "");
  useEffect(() => {
    if (redirect) navigate(redirect + (redirect.includes("#") ? "" : location.hash), { replace: true });
  }, [redirect]);

  let page: Page = "none";
  let screen: ReactNode = null;
  let m: RegExpExecArray | null;
  if (path === "/") (page = "dashboard"), (screen = <Dashboard />);
  else if (path === "/topology") (page = "topology"), (screen = <Topology />);
  else if (path === "/clients") (page = "clients"), (screen = <Clients id={null} tab="overview" />);
  else if ((m = /^\/clients\/([^/]+)(?:\/(overview|insights|settings))?$/.exec(path))) {
    page = "clients";
    const tab = (TABS.includes(m[2] as Tab) ? m[2] : "overview") as Tab;
    screen = <Clients id={decodeURIComponent(m[1])} tab={tab} />;
  } else if (path === "/rules") (page = "rules"), (screen = <Rules id={null} />);
  else if ((m = /^\/rules\/(\d+|new)$/.exec(path))) (page = "rules"), (screen = <Rules key={m[1]} id={m[1] === "new" ? "new" : Number(m[1])} />);
  else if (path === "/activity") (page = "activity"), (screen = <Activity />);
  else if (path === "/internet") (page = "internet"), (screen = <Internet />);
  else if (path === "/health") (page = "health"), (screen = <Health />);
  else if (path === "/settings") (page = "settings"), (screen = <Settings />);

  useEffect(() => {
    document.title = `${path === "/setup" ? "Set up" : TITLE[page]} · ${brand.name}`;
  }, [page, path, brand.name]);

  if (path === "/setup") return <Setup />;

  if (redirect) return <Shell page="none">{null}</Shell>;
  return (
    <Shell page={page}>
      {screen ?? (
        <div className="page">
          <div className="ph">
            <h1>Nothing here</h1>
            <span className="meta">There's no page at {path}.</span>
          </div>
          <Link className="btn" href="/">
            Back to the dashboard
          </Link>
        </div>
      )}
    </Shell>
  );
}
