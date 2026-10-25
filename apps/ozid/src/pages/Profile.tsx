import { useEffect } from "react";
import { ProfileForm } from "@kongken/account/forms";
import { account } from "@/lib/rpc";
import { useSession } from "@/lib/session";
import { RequireSession } from "@/components/RequireSession";

/**
 * The shared nickname and avatar. The login email is shown and cannot be changed here (there
 * is no email change in OzID). Only the account's own fields are edited: the rider profile
 * and every other thing a product keeps for itself stay where they are.
 */
export default function ProfilePage() {
  const { refresh } = useSession();
  useEffect(() => {
    document.title = "资料 · OzID";
  }, []);
  return (
    <RequireSession>
      {({ user, email }) => (
        <div className="mx-auto grid max-w-md gap-6" data-testid="profile-page">
          <div>
            <h1 className="text-xl font-semibold">资料</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              昵称和头像在山海足迹、地铁足迹间共用。地铁足迹里的骑行者主页、简介等资料仍在那个产品里单独编辑，这里不会改动。
            </p>
          </div>
          <ProfileForm key={user.id} api={account} user={user} email={email} onUpdated={() => void refresh(true)} onSessionInvalid={() => void refresh(true)} />
        </div>
      )}
    </RequireSession>
  );
}
