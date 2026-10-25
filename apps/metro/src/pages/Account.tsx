import { useEffect, useState } from "react";
import { ChangePasswordForm, DeleteAccountPanel, ProfileForm, SessionsList } from "@kongken/account/forms";
import { account } from "@/lib/rpc";
import { describeError } from "@/lib/errors";
import { navigate } from "@/lib/router";
import { useSession } from "@/lib/session";
import { Alert, PageTitle, Spinner } from "@/components/extras";

const APP_NAME = "地铁足迹";

/** The signed-in owner's account: profile, password, devices and closing the shared account. */
export default function AccountPage() {
  const { session, signIn, refresh, updateAccount, setDeletionPending, deletionPending } = useSession();
  const [email, setEmail] = useState<string | null>(null);
  const [error, setError] = useState<string | undefined>();

  const signedIn = session.status === "signed-in";
  useEffect(() => {
    if (!signedIn) return;
    let alive = true;
    account
      .me()
      .then((me) => alive && setEmail(me.email))
      .catch((e) => alive && setError(describeError(e)));
    return () => {
      alive = false;
    };
  }, [signedIn]);

  if (session.status === "loading") return <Spinner />;
  if (session.status === "anonymous") {
    return (
      <>
        <PageTitle>账号设置</PageTitle>
        <p className="mb-3 text-sm text-muted-foreground">请先登录。</p>
        <button className="text-sm text-primary underline" onClick={signIn}>去登录</button>
      </>
    );
  }
  const user = { id: session.account.id, displayName: session.account.displayName, avatarUrl: session.account.avatarUrl };
  return (
    <div className="grid max-w-xl gap-8">
      <PageTitle sub="同一个账号也用于山海足迹。">账号设置</PageTitle>
      {error ? <Alert>{error}</Alert> : null}
      {email === null && !error ? (
        <Spinner />
      ) : email !== null ? (
        <>
          <ProfileForm key={user.id} api={account} user={user} email={email} onUpdated={(u) => updateAccount({ displayName: u.displayName, avatarUrl: u.avatarUrl })} />
          <ChangePasswordForm
            api={account}
            email={email}
            onChanged={async () => {
              // Every session of the account stopped working, this one included.
              await refresh();
              navigate("/login?changed=1", true);
            }}
          />
          <SessionsList api={account} identity={user.id} onSignedOutHere={() => void refresh()} />
          <DeleteAccountPanel
            api={account}
            appName={APP_NAME}
            resumePending={deletionPending}
            onPending={() => setDeletionPending(true)}
            onDeleted={async () => {
              setDeletionPending(false);
              await refresh();
              navigate("/login?deleted=1", true);
            }}
          />
        </>
      ) : null}
    </div>
  );
}
