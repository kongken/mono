import { lazy, Suspense } from "react";
import { Spinner } from "@/components/extras";
import { Layout } from "@/components/Layout";
import { useRoute } from "@/lib/router";
import { SessionProvider } from "@/lib/session";
import AccountPage from "@/pages/Account";
import CityPage from "@/pages/City";
import Home from "@/pages/Home";
import LoginPage from "@/pages/Login";
import MePage from "@/pages/Me";
import NotFound from "@/pages/NotFound";
import PublicProfilePage from "@/pages/PublicProfile";
import TopicPage from "@/pages/Topic";

// The administration screens are only needed by administrators, so they load on demand.
const AdminPage = lazy(() => import("@/pages/admin/AdminPage"));

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
      return <LoginPage />;
    case "account":
      return <AccountPage />;
    case "admin":
      return (
        <Suspense fallback={<Spinner />}>
          <AdminPage section={route.section} />
        </Suspense>
      );
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
