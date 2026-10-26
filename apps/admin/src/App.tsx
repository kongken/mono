import { Gate } from "@/components/Gate";
import { Layout } from "@/components/Layout";
import { Spinner } from "@/components/Layout";
import { MetroProvider, useMetro } from "@/lib/metro";
import { Link, useRoute } from "@/lib/router";
import { roleOf, type MetroSection } from "@/lib/routes.ts";
import { Notice } from "@kongken/account/ui";
import { SessionProvider } from "@/lib/session";
import AuditUsersPage from "@/pages/AuditUsers";
import HomePage from "@/pages/Home";
import MetroPage from "@/pages/metro/MetroPage";
import NotFound from "@/pages/NotFound";
import UserDetailPage from "@/pages/UserDetail";
import UsersPage from "@/pages/Users";

/** A metro section, only while metro is there to be administered: without a metro behind the BFF there is nothing to operate. */
function MetroRoute({ section }: { section: MetroSection }) {
  const metro = useMetro();
  if (metro === "checking") return <Spinner />;
  if (metro === "absent")
    return (
      <section className="grid max-w-lg gap-3" data-testid="metro-absent">
        <h1 className="text-xl font-semibold">地铁目录</h1>
        <Notice kind="info">这个环境没有部署地铁目录服务，这里没有可以管理的内容。</Notice>
        <Link to="/" className="text-sm text-primary underline-offset-4 hover:underline">回到控制台首页</Link>
      </section>
    );
  return <MetroPage section={section} />;
}

function Routes() {
  const route = useRoute();
  // An unknown address shows no management data and asks nobody to sign in.
  if (route.name === "not-found") return <NotFound />;
  return (
    <Gate role={roleOf(route)}>
      {route.name === "home" ? (
        <HomePage />
      ) : route.name === "users" ? (
        <UsersPage />
      ) : route.name === "audit-users" ? (
        <AuditUsersPage />
      ) : route.name === "metro" ? (
        <MetroRoute section={route.section} />
      ) : (
        <UserDetailPage id={route.id} />
      )}
    </Gate>
  );
}

export default function App() {
  return (
    <SessionProvider>
      <MetroProvider>
        <Layout>
          <Routes />
        </Layout>
      </MetroProvider>
    </SessionProvider>
  );
}
