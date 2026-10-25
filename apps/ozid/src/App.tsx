import { Layout } from "@/components/Layout";
import { useRoute } from "@/lib/router";
import { SessionProvider } from "@/lib/session";
import LoginPage from "@/pages/Login";
import NotFound from "@/pages/NotFound";
import OverviewPage from "@/pages/Overview";
import SecurityPage from "@/pages/Security";

function Routes() {
  switch (useRoute()) {
    case "overview":
      return <OverviewPage />;
    case "login":
      return <LoginPage />;
    case "security":
      return <SecurityPage />;
    default:
      return <NotFound />;
  }
}

export default function App() {
  return (
    <SessionProvider>
      <Layout>
        <Routes />
      </Layout>
    </SessionProvider>
  );
}
