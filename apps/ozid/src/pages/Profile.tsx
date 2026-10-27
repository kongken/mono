import { useEffect } from "react";
import { ProfileForm } from "@kongken/account/forms";
import { account } from "@/lib/rpc";
import { useSession } from "@/lib/session";
import { RequireSession } from "@/components/RequireSession";
import { PageHeader } from "@/components/Layout";
import { UsersThree } from "@phosphor-icons/react";

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
        <div className="grid gap-6" data-testid="profile-page">
          <PageHeader eyebrow="OzID 统一身份管理" title="资料">
            管理在接入应用中共用的基本信息。
          </PageHeader>
          <section className="relative flex items-start gap-3.5 overflow-hidden rounded-xl bg-muted p-5 shadow-sm">
            <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-card text-signal shadow-sm">
              <UsersThree className="size-5" weight="bold" aria-hidden />
            </span>
            <div className="grid gap-1">
              <h2 className="text-sm font-semibold text-primary">共用范围说明</h2>
              <p className="text-sm leading-relaxed text-muted-foreground">
                昵称和头像在山海足迹、地铁足迹间共用。地铁足迹里的骑行者主页、简介等资料仍在那个产品里单独编辑，这里不会改动。
              </p>
            </div>
          </section>
          <ProfileForm key={user.id} api={account} user={user} email={email} onUpdated={() => void refresh(true)} onSessionInvalid={() => void refresh(true)} />
        </div>
      )}
    </RequireSession>
  );
}
