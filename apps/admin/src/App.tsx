import { Gate } from "@/components/Gate";
import { Layout } from "@/components/Layout";
import { useRoute } from "@/lib/router";
import { roleOf } from "@/lib/routes.ts";
import { SessionProvider } from "@/lib/session";
import AuditUsersPage from "@/pages/AuditUsers";
import HomePage from "@/pages/Home";
import NotFound from "@/pages/NotFound";
import UserDetailPage from "@/pages/UserDetail";
import UsersPage from "@/pages/Users";

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
      ) : (
        <UserDetailPage id={route.id} />
      )}
    </Gate>
  );
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
