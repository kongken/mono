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
        <div className="grid max-w-2xl gap-6" data-testid="profile-page">
          <PageHeader eyebrow="OzID 统一身份管理" title="资料">
            管理在接入应用中共用的基本信息。
          </PageHeader>
          <section className="relative flex items-start gap-3.5 overflow-hidden rounded-xl bg-muted p-5 shadow-sm">
            <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-card text-signal shadow-sm">
              <UsersThree className="size-5" weight="bold" aria-hidden />
            </span>
            <p className="text-sm leading-relaxed text-muted-foreground">
              昵称和头像在山海足迹、地铁足迹间共用。地铁足迹里的骑行者主页、简介等资料仍在那个产品里单独编辑，这里不会改动。
            </p>
          </section>
          <div className="rounded-xl bg-card p-6 shadow-sm sm:p-8">
            <ProfileForm key={user.id} api={account} user={user} email={email} onUpdated={() => void refresh(true)} onSessionInvalid={() => void refresh(true)} />
          </div>
        </div>
      )}
    </RequireSession>
  );
}
