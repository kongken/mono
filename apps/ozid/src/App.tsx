import { Layout } from "@/components/Layout";
import { useRoute } from "@/lib/router";
import { SessionProvider, useSession } from "@/lib/session";
import HomePage from "@/pages/Home";
import CloseAccountPage from "@/pages/CloseAccount";
import ForgotPasswordPage from "@/pages/ForgotPassword";
import LoginPage from "@/pages/Login";
import NotFound from "@/pages/NotFound";
import OverviewPage from "@/pages/Overview";
import ProfilePage from "@/pages/Profile";
import RegisterPage from "@/pages/Register";
import SecurityPage from "@/pages/Security";
import SessionsPage from "@/pages/Sessions";

function Routes() {
  const { session } = useSession();
  switch (useRoute()) {
    case "overview":
      return session.status === "signed-in" ? <OverviewPage /> : <HomePage />;
    case "login":
      return <LoginPage />;
    case "register":
      return <RegisterPage />;
    case "forgot-password":
      return <ForgotPasswordPage />;
    case "profile":
      return <ProfilePage />;
    case "security":
      return <SecurityPage />;
    case "sessions":
      return <SessionsPage />;
    case "close-account":
      return <CloseAccountPage />;
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
