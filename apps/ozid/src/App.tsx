import { Layout } from "@/components/Layout";
import { useRoute } from "@/lib/router";
import { SessionProvider } from "@/lib/session";
import ForgotPasswordPage from "@/pages/ForgotPassword";
import LoginPage from "@/pages/Login";
import NotFound from "@/pages/NotFound";
import OverviewPage from "@/pages/Overview";
import RegisterPage from "@/pages/Register";
import SecurityPage from "@/pages/Security";

function Routes() {
  switch (useRoute()) {
    case "overview":
      return <OverviewPage />;
    case "login":
      return <LoginPage />;
    case "register":
      return <RegisterPage />;
    case "forgot-password":
      return <ForgotPasswordPage />;
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
