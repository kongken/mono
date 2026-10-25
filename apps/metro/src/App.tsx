import { Layout } from "@/components/Layout";
import { useRoute } from "@/lib/router";
import { accountId, SessionProvider, useSession } from "@/lib/session";
import CityPage from "@/pages/City";
import Home from "@/pages/Home";
import LegacyAccountPage from "@/pages/LegacyAccount";
import MePage from "@/pages/Me";
import NotFound from "@/pages/NotFound";
import PublicProfilePage from "@/pages/PublicProfile";
import TopicPage from "@/pages/Topic";

function Routes() {
  const route = useRoute();
  switch (route.name) {
    case "home":
      return <Home />;
    case "city":
      return <CityPage key={route.slug} slug={route.slug} />;
    case "topic":
      return <TopicPage key={route.slug} slug={route.slug} />;
    case "profile":
      return <PublicProfilePage key={route.handle} handle={route.handle} />;
    case "me":
      return <MePage />;
    case "login":
    case "account":
      return <LegacyAccountPage kind={route.name} />;
    default:
      return <NotFound />;
  }
}

/**
 * The pages hold the signed-in person's own data (marked lines, the rider profile). Keying them by the account
 * means a change of account, seen when the shared session is next checked, starts them over instead of showing
 * the previous account's data for a moment.
 */
function Pages() {
  const { session } = useSession();
  return (
    <Layout>
      <Routes key={accountId(session) ?? "none"} />
    </Layout>
  );
}

export default function App() {
  return (
    <SessionProvider>
      <Pages />
    </SessionProvider>
  );
}
