import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import {
  Check,
  Copy,
  Eye,
  Plus,
  Trash,
  UploadSimple,
  Users,
} from "@phosphor-icons/react";
import {
  collaborationApi,
  type CollaborationOverview,
  type FriendFootprintView,
  type SharedTripInfo,
  type SharedTripMediaInfo,
} from "@/api/collaboration";
import { mediaApi } from "@/api/media";
import { Button } from "@trip-map/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@trip-map/ui/dialog";
import { Input } from "@trip-map/ui/input";
import type { Cloud } from "@/hooks/useCloud";
import {
  canEditSharedTrip,
  canManageSharedTripMembers,
  friendLabel,
  normalizeFriendSettings,
  provinceNameByCode,
  roleLabel,
  type FriendPrivacySettings,
} from "@/lib/collaboration";
import { describeApiError } from "@/lib/errors";
import type { Saved } from "@/lib/travel";
import { TRIP_NOTE_LIMIT } from "@/lib/trips";
import "./CollaborationDialog.css";

export type CollaborationTarget = {
  regionCode: string;
  tripId: string;
} | null;

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cloud: Cloud;
  saved: Saved;
  initialTarget?: CollaborationTarget;
  onOverviewChange?: (overview: CollaborationOverview) => void;
};

function SharedMediaThumb({
  sharedTripId,
  item,
}: {
  sharedTripId: string;
  item: SharedTripMediaInfo;
}) {
  const [url, setUrl] = useState<string>("");
  useEffect(() => {
    let active = true;
    let objectUrl = "";
    const mediaId = item.media?.id ?? "";
    if (!mediaId) return;
    void collaborationApi
      .readSharedTripMedia(sharedTripId, mediaId, true)
      .then((res) => {
        if (!active) return;
        objectUrl = URL.createObjectURL(res.blob);
        setUrl(objectUrl);
      })
      .catch(() => {});
    return () => {
      active = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [sharedTripId, item.media?.id]);

  return (
    <div className="collab-media-card">
      {url ? (
        <img src={url} alt={item.media?.filename || "共同旅行照片"} />
      ) : (
        <div style={{ height: 76, background: "#eef4f0", borderRadius: 6 }} />
      )}
      <span title={item.media?.filename}>{item.media?.filename}</span>
      <small>由 {friendLabel(item.uploader)} 上传</small>
    </div>
  );
}

export default function CollaborationDialog({
  open,
  onOpenChange,
  cloud,
  saved,
  initialTarget = null,
  onOverviewChange,
}: Props) {
  const [tab, setTab] = useState<"friends" | "trips">(
    initialTarget ? "trips" : "friends",
  );
  const [overview, setOverview] = useState<CollaborationOverview | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copiedCode, setCopiedCode] = useState(false);

  // Add friend form
  const [friendTarget, setFriendTarget] = useState("");
  const [confirmUnfriendId, setConfirmUnfriendId] = useState<string | null>(null);

  // Friend map comparison view
  const [selectedFriendId, setSelectedFriendId] = useState<string | null>(null);
  const [friendView, setFriendView] = useState<FriendFootprintView | null>(null);

  // Invite to shared trip form
  const [selectedTripKey, setSelectedTripKey] = useState("");
  const [inviteFriendId, setInviteFriendId] = useState("");
  const [inviteRole, setInviteRole] = useState<"editor" | "viewer">("editor");

  // Shared trip edits & confirmations
  const [tripDrafts, setTripDrafts] = useState<
    Record<string, { date: string; note: string }>
  >({});
  const [confirmLeaveId, setConfirmLeaveId] = useState<string | null>(null);
  const [confirmDissolveId, setConfirmDissolveId] = useState<string | null>(null);
  const [sharedMediaByTrip, setSharedMediaByTrip] = useState<
    Record<string, SharedTripMediaInfo[]>
  >({});

  const syncedVisits = useMemo(
    () => cloud.confirmedSaved()?.visits ?? saved.visits,
    [cloud, saved.visits],
  );

  const syncedTripOptions = useMemo(() => {
    const out: { key: string; regionCode: string; cityName: string; tripId: string; label: string }[] = [];
    for (const v of syncedVisits) {
      v.trips.forEach((t, idx) => {
        const key = `${v.code}:${t.id}`;
        const summary = t.date || (t.note ? t.note.slice(0, 14) : `第 ${idx + 1} 次旅行`);
        out.push({
          key,
          regionCode: v.code,
          cityName: v.name,
          tripId: t.id,
          label: `${v.name} · ${summary}`,
        });
      });
    }
    return out;
  }, [syncedVisits]);

  const syncDraftsFromOverview = useCallback((ov: CollaborationOverview) => {
    setTripDrafts((prev) => {
      const next = { ...prev };
      for (const st of ov.sharedTrips) {
        if (!next[st.id]) {
          next[st.id] = { date: st.date, note: st.note };
        }
      }
      return next;
    });
  }, []);

  const loadOverview = useCallback(async () => {
    setLoading(true);
    try {
      const ov = await collaborationApi.getOverview();
      setOverview(ov);
      syncDraftsFromOverview(ov);
      onOverviewChange?.(ov);
      return ov;
    } catch (e) {
      setError(describeApiError(e));
      return null;
    } finally {
      setLoading(false);
    }
  }, [onOverviewChange, syncDraftsFromOverview]);

  useEffect(() => {
    if (!open) return;
    setError("");
    setNotice("");
    setConfirmUnfriendId(null);
    setConfirmLeaveId(null);
    setConfirmDissolveId(null);
    if (initialTarget) {
      setTab("trips");
      setSelectedTripKey(`${initialTarget.regionCode}:${initialTarget.tripId}`);
    } else {
      setTab("friends");
    }
    void loadOverview();
  }, [open, initialTarget, loadOverview]);

  const confirmedFriends = useMemo(
    () => (overview?.friends ?? []).filter((f) => f.status === "accepted"),
    [overview],
  );
  const pendingFriends = useMemo(
    () => (overview?.friends ?? []).filter((f) => f.status === "pending"),
    [overview],
  );

  useEffect(() => {
    if (!selectedTripKey && syncedTripOptions.length > 0) {
      setSelectedTripKey(syncedTripOptions[0].key);
    }
  }, [selectedTripKey, syncedTripOptions]);

  useEffect(() => {
    if (!inviteFriendId && confirmedFriends.length > 0) {
      setInviteFriendId(confirmedFriends[0].friend?.id ?? "");
    }
  }, [inviteFriendId, confirmedFriends]);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await fn();
    } catch (e) {
      setError(describeApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const handleTogglePrivacy = (patch: Partial<FriendPrivacySettings>) => {
    if (!overview) return;
    const prevOverview = overview;
    const next = normalizeFriendSettings({
      ...overview.friendSettings,
      ...patch,
    });
    const optimistic = { ...overview, friendSettings: next };
    setOverview(optimistic);
    onOverviewChange?.(optimistic);
    void run(async () => {
      try {
        const savedSettings = await collaborationApi.updateFriendSettings(next);
        const updated = { ...optimistic, friendSettings: savedSettings };
        setOverview(updated);
        onOverviewChange?.(updated);
        setNotice("已更新对好友公开的内容范围");
      } catch (e) {
        setOverview(prevOverview);
        onOverviewChange?.(prevOverview);
        throw e;
      }
    });
  };

  const handleSendFriendRequest = (e: FormEvent) => {
    e.preventDefault();
    const target = friendTarget.trim();
    if (!target) return;
    void run(async () => {
      await collaborationApi.sendFriendRequest(target);
      setFriendTarget("");
      await loadOverview();
      setNotice("好友申请已发送");
    });
  };

  const handleViewFriendMap = (friendUserId: string) => {
    void run(async () => {
      const view = await collaborationApi.getFriendFootprints(friendUserId);
      setSelectedFriendId(friendUserId);
      setFriendView(view);
    });
  };

  const handleInviteToTrip = (e: FormEvent) => {
    e.preventDefault();
    const opt = syncedTripOptions.find((o) => o.key === selectedTripKey);
    if (!opt || !inviteFriendId) return;
    void run(async () => {
      await collaborationApi.inviteToSharedTrip({
        regionCode: opt.regionCode,
        tripId: opt.tripId,
        friendUserId: inviteFriendId,
        role: inviteRole,
      });
      await loadOverview();
      setNotice(`已邀请好友加入 ${opt.cityName} 的共同旅行`);
    });
  };

  const handleLoadSharedMedia = (st: SharedTripInfo) => {
    void run(async () => {
      const list = await collaborationApi.listSharedTripMedia(st.id);
      setSharedMediaByTrip((prev) => ({ ...prev, [st.id]: list }));
    });
  };

  const handleUploadSharedPhoto = (st: SharedTripInfo, file: File) => {
    void run(async () => {
      const uploadId = crypto.randomUUID();
      const prep = await mediaApi.prepare(st.regionCode, file, uploadId, st.tripId);
      if (prep.uploadUrl) {
        const headers = new Headers(prep.headers);
        const putRes = await fetch(prep.uploadUrl, {
          method: "PUT",
          headers,
          body: file,
        });
        if (!putRes.ok) throw new Error("upload failed");
      }
      if (prep.media?.id) {
        await mediaApi.complete(prep.media.id);
      }
      const list = await collaborationApi.listSharedTripMedia(st.id);
      setSharedMediaByTrip((prev) => ({ ...prev, [st.id]: list }));
      setNotice("共同旅行照片已上传");
    });
  };

  const invitedTrips = useMemo(
    () => (overview?.sharedTrips ?? []).filter((t) => t.myStatus === "invited"),
    [overview],
  );
  const activeTrips = useMemo(
    () => (overview?.sharedTrips ?? []).filter((t) => t.myStatus === "active"),
    [overview],
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="collab-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <Users size={22} />
          </span>
          <DialogTitle>好友与共同旅行</DialogTitle>
          <DialogDescription>
            好友关系需双方确认。对比地图只展示各自选择公开的内容；退出共同旅行不会删除自己或他人的城市足迹。
          </DialogDescription>
        </DialogHeader>

        <div className="collab-tabs" role="tablist" aria-label="协作功能切换">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "friends"}
            className={tab === "friends" ? "nav-active" : ""}
            onClick={() => setTab("friends")}
          >
            好友与足迹对比
            {pendingFriends.length > 0 ? `（${pendingFriends.length}）` : ""}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "trips"}
            className={tab === "trips" ? "nav-active" : ""}
            onClick={() => setTab("trips")}
          >
            共同旅行
            {invitedTrips.length > 0 ? `（${invitedTrips.length} 待确认）` : ""}
          </button>
        </div>

        {error && (
          <p className="unreadable-note unreadable-error" role="alert">
            {error}
          </p>
        )}
        {notice && (
          <p className="unreadable-note" role="status">
            {notice}
          </p>
        )}

        {tab === "friends" && (
          <div>
            <section className="collab-section" aria-label="我的邀请码与公开设置">
              <h3>我的好友邀请码与公开范围</h3>
              <p className="collab-hint">
                可将用户名或邀请码发给朋友。已确认的好友默认可查看你的点亮省份与城市；旅行日期与备注需你主动勾选公开。
              </p>
              {overview?.myFriendCode && (
                <div className="collab-code-row">
                  <Input
                    readOnly
                    aria-label="我的好友邀请码"
                    value={overview.myFriendCode}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      void navigator.clipboard?.writeText(overview.myFriendCode);
                      setCopiedCode(true);
                      setTimeout(() => setCopiedCode(false), 1800);
                    }}
                  >
                    {copiedCode ? <Check size={15} /> : <Copy size={15} />}
                    {copiedCode ? "已复制" : "复制邀请码"}
                  </Button>
                </div>
              )}
              {overview && (
                <div className="collab-checks">
                  <label>
                    <input
                      type="checkbox"
                      checked={overview.friendSettings.showCities}
                      disabled={busy}
                      onChange={(e) =>
                        handleTogglePrivacy({ showCities: e.target.checked })
                      }
                    />
                    对好友公开打卡城市与颜色（双方均开启时支持共同打卡城市对比）
                  </label>
                  <label
                    className={
                      !overview.friendSettings.showCities ? "is-disabled" : ""
                    }
                  >
                    <input
                      type="checkbox"
                      checked={overview.friendSettings.showDates}
                      disabled={busy || !overview.friendSettings.showCities}
                      onChange={(e) =>
                        handleTogglePrivacy({ showDates: e.target.checked })
                      }
                    />
                    对好友公开旅行日期
                  </label>
                  <label
                    className={
                      !overview.friendSettings.showCities ? "is-disabled" : ""
                    }
                  >
                    <input
                      type="checkbox"
                      checked={overview.friendSettings.showNotes}
                      disabled={busy || !overview.friendSettings.showCities}
                      onChange={(e) =>
                        handleTogglePrivacy({ showNotes: e.target.checked })
                      }
                    />
                    对好友公开旅行记忆（备注）
                  </label>
                </div>
              )}
            </section>

            <section className="collab-section" aria-label="添加好友">
              <h3>添加好友</h3>
              <form className="collab-form-row" onSubmit={handleSendFriendRequest}>
                <Input
                  aria-label="好友用户名或邀请码"
                  placeholder="输入对方用户名或 fc_ 邀请码"
                  value={friendTarget}
                  onChange={(e) => setFriendTarget(e.target.value)}
                  required
                />
                <Button type="submit" size="sm" disabled={busy || !friendTarget.trim()}>
                  <Plus size={15} />
                  发送好友申请
                </Button>
              </form>
            </section>

            {pendingFriends.length > 0 && (
              <section className="collab-section" aria-label="待确认的好友申请">
                <h3>待确认的好友申请</h3>
                <ul className="collab-list">
                  {pendingFriends.map((f) => (
                    <li key={f.id} className="collab-item">
                      <div className="collab-item-info">
                        <strong>{friendLabel(f.friend)}</strong>
                        <small>
                          {f.direction === "incoming"
                            ? "对方请求添加你为好友"
                            : "已发送申请，等待对方确认"}
                        </small>
                      </div>
                      <div className="collab-item-actions">
                        {f.direction === "incoming" ? (
                          <>
                            <Button
                              size="sm"
                              disabled={busy}
                              onClick={() =>
                                void run(async () => {
                                  await collaborationApi.respondFriendRequest(
                                    f.id,
                                    true,
                                  );
                                  await loadOverview();
                                  setNotice(
                                    `已与 ${friendLabel(f.friend)} 成为好友`,
                                  );
                                })
                              }
                            >
                              接受好友申请
                            </Button>
                            <Button
                              variant="outline"
                              size="sm"
                              disabled={busy}
                              onClick={() =>
                                void run(async () => {
                                  await collaborationApi.respondFriendRequest(
                                    f.id,
                                    false,
                                  );
                                  await loadOverview();
                                  setNotice("已拒绝好友申请");
                                })
                              }
                            >
                              拒绝
                            </Button>
                          </>
                        ) : (
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={busy}
                            onClick={() =>
                              void run(async () => {
                                await collaborationApi.respondFriendRequest(
                                  f.id,
                                  false,
                                );
                                await loadOverview();
                                setNotice("已撤回好友申请");
                              })
                            }
                          >
                            撤回申请
                          </Button>
                        )}
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section className="collab-section" aria-label="我的好友列表">
              <h3>我的好友（{confirmedFriends.length}）</h3>
              {loading && !overview ? (
                <p className="collab-hint">正在加载好友列表…</p>
              ) : confirmedFriends.length === 0 ? (
                <p className="collab-hint">
                  还没有已确认的好友。发送申请并由对方确认后，即可在这里查看对方地图与双方足迹对比。
                </p>
              ) : (
                <ul className="collab-list">
                  {confirmedFriends.map((f) => {
                    const fid = f.friend?.id ?? "";
                    const isConfirming = confirmUnfriendId === f.id;
                    return (
                      <li key={f.id} className="collab-item">
                        <div className="collab-item-info">
                          <strong>{friendLabel(f.friend)}</strong>
                          {f.friend?.username && (
                            <small>@{f.friend.username}</small>
                          )}
                        </div>
                        <div className="collab-item-actions">
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={busy}
                            onClick={() => handleViewFriendMap(fid)}
                          >
                            <Eye size={15} />
                            查看地图与对比
                          </Button>
                          {isConfirming ? (
                            <>
                              <Button
                                variant="destructive"
                                size="sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(async () => {
                                    await collaborationApi.removeFriend(f.id);
                                    if (selectedFriendId === fid) {
                                      setSelectedFriendId(null);
                                      setFriendView(null);
                                    }
                                    setConfirmUnfriendId(null);
                                    await loadOverview();
                                    setNotice(
                                      `已与 ${friendLabel(f.friend)} 解除好友关系`,
                                    );
                                  })
                                }
                              >
                                确认解除好友
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={busy}
                                onClick={() => setConfirmUnfriendId(null)}
                              >
                                取消
                              </Button>
                            </>
                          ) : (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={busy}
                              onClick={() => setConfirmUnfriendId(f.id)}
                            >
                              解除好友
                            </Button>
                          )}
                        </div>
                      </li>
                    );
                  })}
                </ul>
              )}

              {friendView && selectedFriendId && (
                <section
                  className="collab-comparison"
                  aria-label={`${friendLabel(friendView.friend)}的足迹对比`}
                >
                  <div className="collab-trip-head">
                    <h4>
                      与 {friendLabel(friendView.friend)} 的足迹对比
                    </h4>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setSelectedFriendId(null);
                        setFriendView(null);
                      }}
                    >
                      收起对比
                    </Button>
                  </div>
                  <p className="collab-hint">
                    对方已探索 {friendView.content.stats.explored_provinces} /{" "}
                    {friendView.content.stats.total_provinces} 个省级区域
                    {friendView.friendSettings.showCities
                      ? ` · ${friendView.content.stats.cities} 座城市 · ${friendView.content.stats.trips} 次旅行`
                      : "（对方未公开具体城市）"}
                  </p>

                  {friendView.comparison && (
                    <div className="collab-stat-grid">
                      <div className="collab-stat-box">
                        <strong>
                          共同探索省份（
                          {friendView.comparison.commonProvinces.length}）
                        </strong>
                        <div className="collab-chip-list">
                          {friendView.comparison.commonProvinces.length === 0 ? (
                            <span>暂无</span>
                          ) : (
                            friendView.comparison.commonProvinces.map((code) => (
                              <span key={code} className="collab-chip">
                                <i style={{ background: "#4b937d" }} />
                                {provinceNameByCode(code)}
                              </span>
                            ))
                          )}
                        </div>
                      </div>
                      <div className="collab-stat-box">
                        <strong>
                          仅我探索省份（
                          {friendView.comparison.onlyMeProvinces.length}）
                        </strong>
                        <div className="collab-chip-list">
                          {friendView.comparison.onlyMeProvinces.length === 0 ? (
                            <span>暂无</span>
                          ) : (
                            friendView.comparison.onlyMeProvinces.map((code) => (
                              <span key={code} className="collab-chip">
                                <i style={{ background: "#719fc6" }} />
                                {provinceNameByCode(code)}
                              </span>
                            ))
                          )}
                        </div>
                      </div>
                      <div className="collab-stat-box">
                        <strong>
                          仅对方探索省份（
                          {friendView.comparison.onlyFriendProvinces.length}）
                        </strong>
                        <div className="collab-chip-list">
                          {friendView.comparison.onlyFriendProvinces.length ===
                          0 ? (
                            <span>暂无</span>
                          ) : (
                            friendView.comparison.onlyFriendProvinces.map(
                              (code) => (
                                <span key={code} className="collab-chip">
                                  <i style={{ background: "#e2af5c" }} />
                                  {provinceNameByCode(code)}
                                </span>
                              ),
                            )
                          )}
                        </div>
                      </div>
                    </div>
                  )}

                  {friendView.comparison?.cityComparisonAvailable ? (
                    <div className="collab-stat-box" style={{ marginTop: 8 }}>
                      <strong>
                        共同打卡城市（
                        {friendView.comparison.commonCities.length}） · 仅我打卡{" "}
                        {friendView.comparison.myOnlyCityCount} 座 · 仅对方打卡{" "}
                        {friendView.comparison.friendOnlyCityCount} 座
                      </strong>
                      <div className="collab-chip-list">
                        {friendView.comparison.commonCities.length === 0 ? (
                          <span>暂无共同打卡城市</span>
                        ) : (
                          friendView.comparison.commonCities.map((c) => (
                            <span key={c.code} className="collab-chip">
                              <i style={{ background: c.friendColor }} />
                              {c.name}
                            </span>
                          ))
                        )}
                      </div>
                    </div>
                  ) : (
                    <p className="collab-hint" style={{ marginTop: 8 }}>
                      需要双方均开启“公开打卡城市”后才能查看共同打卡城市对比。
                    </p>
                  )}

                  {friendView.friendSettings.showCities &&
                    friendView.content.cities.length > 0 && (
                      <div style={{ marginTop: 10 }}>
                        <strong>
                          {friendLabel(friendView.friend)} 公开的城市足迹
                        </strong>
                        <ul className="collab-list">
                          {friendView.content.cities.map((c) => (
                            <li key={c.code} className="collab-item">
                              <div className="collab-item-info">
                                <strong>
                                  {c.name}（{provinceNameByCode(c.province)}）
                                </strong>
                                {c.trips.map((t, idx) => (
                                  <small key={idx}>
                                    {t.date || "日期未公开或未记录"}
                                    {t.note ? ` · ${t.note}` : ""}
                                  </small>
                                ))}
                              </div>
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                </section>
              )}
            </section>
          </div>
        )}

        {tab === "trips" && (
          <div>
            <section className="collab-section" aria-label="邀请好友加入共同旅行">
              <h3>邀请好友加入共同旅行</h3>
              <p className="collab-hint">
                选择一次已同步到云端的城市旅行并邀请已确认的好友。对方接受后，双方在各自地图中都会看到这次旅行。
              </p>
              {syncedTripOptions.length === 0 ? (
                <p className="collab-hint">
                  当前还没有已同步到云端的旅行记录。请先在地图中添加城市旅行并完成同步。
                </p>
              ) : confirmedFriends.length === 0 ? (
                <p className="collab-hint">
                  还没有已确认的好友。请先在“好友与足迹对比”标签页添加好友并等待对方确认。
                </p>
              ) : (
                <form className="collab-form-row" onSubmit={handleInviteToTrip}>
                  <select
                    aria-label="选择要共享的旅行"
                    className="collab-select"
                    value={selectedTripKey}
                    onChange={(e) => setSelectedTripKey(e.target.value)}
                  >
                    {syncedTripOptions.map((opt) => (
                      <option key={opt.key} value={opt.key}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                  <select
                    aria-label="选择要邀请的好友"
                    className="collab-select"
                    value={inviteFriendId}
                    onChange={(e) => setInviteFriendId(e.target.value)}
                  >
                    {confirmedFriends.map((f) => (
                      <option key={f.friend?.id} value={f.friend?.id}>
                        {friendLabel(f.friend)}
                      </option>
                    ))}
                  </select>
                  <select
                    aria-label="选择成员权限"
                    className="collab-select"
                    value={inviteRole}
                    onChange={(e) =>
                      setInviteRole(e.target.value as "editor" | "viewer")
                    }
                  >
                    <option value="editor">共同编辑（可改日期备注与传照片）</option>
                    <option value="viewer">只读成员（仅查看记录与照片）</option>
                  </select>
                  <Button type="submit" size="sm" disabled={busy}>
                    <Plus size={15} />
                    发送共同旅行邀请
                  </Button>
                </form>
              )}
            </section>

            {invitedTrips.length > 0 && (
              <section className="collab-section" aria-label="待确认的共同旅行邀请">
                <h3>待确认的共同旅行邀请</h3>
                <ul className="collab-list">
                  {invitedTrips.map((st) => (
                    <li key={st.id} className="collab-item">
                      <div className="collab-item-info">
                        <strong>
                          {st.regionName} · 来自 {friendLabel(st.owner)} 的邀请
                        </strong>
                        <small>
                          角色：{roleLabel(st.myRole)} ·{" "}
                          {st.date || "日期未记录"}
                          {st.note ? ` · ${st.note}` : ""}
                        </small>
                      </div>
                      <div className="collab-item-actions">
                        <Button
                          size="sm"
                          disabled={busy}
                          onClick={() =>
                            void run(async () => {
                              await collaborationApi.respondSharedTripInvite(
                                st.id,
                                true,
                              );
                              void cloud.retry();
                              await loadOverview();
                              setNotice(
                                `已加入 ${st.regionName} 的共同旅行，足迹已同步到你的地图`,
                              );
                            })
                          }
                        >
                          接受并加入
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={busy}
                          onClick={() =>
                            void run(async () => {
                              await collaborationApi.respondSharedTripInvite(
                                st.id,
                                false,
                              );
                              await loadOverview();
                              setNotice("已拒绝共同旅行邀请");
                            })
                          }
                        >
                          拒绝
                        </Button>
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section className="collab-section" aria-label="已加入的共同旅行">
              <h3>共同旅行列表（{activeTrips.length}）</h3>
              {activeTrips.length === 0 ? (
                <p className="collab-hint">
                  暂无已加入的共同旅行。你可以邀请好友加入某次旅行，或接受好友发来的共同旅行邀请。
                </p>
              ) : (
                activeTrips.map((st) => {
                  const editable = canEditSharedTrip(st.myRole, st.myStatus);
                  const isOwner = canManageSharedTripMembers(
                    st.myRole,
                    st.myStatus,
                  );
                  const draft = tripDrafts[st.id] ?? {
                    date: st.date,
                    note: st.note,
                  };
                  const mediaList = sharedMediaByTrip[st.id];
                  return (
                    <div
                      key={st.id}
                      className="collab-trip-card"
                      data-shared-trip-id={st.id}
                    >
                      <div className="collab-trip-head">
                        <div>
                          <strong>
                            {st.regionName}（{provinceNameByCode(st.provinceCode)}）
                          </strong>{" "}
                          <span
                            className={`collab-badge${st.myRole === "viewer" ? " is-viewer" : ""}`}
                          >
                            {roleLabel(st.myRole)}
                          </span>
                        </div>
                        <small>创建者：{friendLabel(st.owner)}</small>
                      </div>

                      {editable ? (
                        <div className="collab-trip-fields">
                          <label className="field-label">
                            共同旅行日期（{st.regionName}）
                            <Input
                              type="date"
                              aria-label={`共同旅行日期（${st.regionName}）`}
                              value={draft.date}
                              onChange={(e) =>
                                setTripDrafts((prev) => ({
                                  ...prev,
                                  [st.id]: { ...draft, date: e.target.value },
                                }))
                              }
                            />
                          </label>
                          <label className="field-label">
                            共同旅行记忆（{st.regionName}）
                            <textarea
                              aria-label={`共同旅行记忆（${st.regionName}）`}
                              maxLength={TRIP_NOTE_LIMIT}
                              value={draft.note}
                              onChange={(e) =>
                                setTripDrafts((prev) => ({
                                  ...prev,
                                  [st.id]: { ...draft, note: e.target.value },
                                }))
                              }
                            />
                          </label>
                          <div className="collab-item-actions">
                            <Button
                              size="sm"
                              disabled={busy}
                              onClick={() =>
                                void run(async () => {
                                  await collaborationApi.updateSharedTrip({
                                    sharedTripId: st.id,
                                    date: draft.date,
                                    note: draft.note.trim(),
                                    mutationId: crypto.randomUUID(),
                                  });
                                  void cloud.retry();
                                  await loadOverview();
                                  setNotice(
                                    `已同步更新 ${st.regionName} 的共同旅行`,
                                  );
                                })
                              }
                            >
                              <Check size={15} />
                              保存共同旅行修改
                            </Button>
                          </div>
                        </div>
                      ) : (
                        <div className="collab-trip-fields">
                          <p className="collab-hint">
                            日期：{st.date || "未记录"} · 备注：
                            {st.note || "暂无备注"}
                          </p>
                          <p className="collab-hint">
                            你是只读成员，可查看共同旅行内容与照片，不能修改记录或上传照片。
                          </p>
                        </div>
                      )}

                      <div style={{ marginTop: 8 }}>
                        <div className="collab-item-actions">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={busy}
                            onClick={() => handleLoadSharedMedia(st)}
                          >
                            查看共同旅行照片
                          </Button>
                          {editable && (
                            <label className="collab-upload-btn">
                              <input
                                type="file"
                                accept="image/jpeg,image/png,image/webp"
                                style={{ display: "none" }}
                                aria-label={`上传共同旅行照片（${st.regionName}）`}
                                disabled={busy}
                                onChange={(e) => {
                                  const f = e.target.files?.[0];
                                  e.target.value = "";
                                  if (f) handleUploadSharedPhoto(st, f);
                                }}
                              />
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                asChild
                              >
                                <span>
                                  <UploadSimple size={15} />
                                  上传共同旅行照片
                                </span>
                              </Button>
                            </label>
                          )}
                        </div>
                        {mediaList && (
                          <div className="collab-media-grid">
                            {mediaList.length === 0 ? (
                              <small className="collab-hint">
                                暂无共同旅行照片
                              </small>
                            ) : (
                              mediaList.map((item) => (
                                <SharedMediaThumb
                                  key={item.media?.id}
                                  sharedTripId={st.id}
                                  item={item}
                                />
                              ))
                            )}
                          </div>
                        )}
                      </div>

                      <div style={{ marginTop: 10 }}>
                        <strong style={{ fontSize: "0.84rem" }}>
                          旅行成员（{st.members.length}）
                        </strong>
                        <ul className="collab-list">
                          {st.members.map((m) => {
                            const mid = m.user?.id ?? "";
                            const memberIsOwner = m.role === "owner";
                            return (
                              <li key={mid} className="collab-item">
                                <div className="collab-item-info">
                                  <strong>{friendLabel(m.user)}</strong>
                                  <small>
                                    {roleLabel(m.role)} ·{" "}
                                    {m.status === "active"
                                      ? "已加入"
                                      : "待接受邀请"}
                                  </small>
                                </div>
                                {isOwner && !memberIsOwner && (
                                  <div className="collab-item-actions">
                                    <select
                                      aria-label={`${friendLabel(m.user)}的权限`}
                                      className="collab-select"
                                      value={m.role}
                                      disabled={busy}
                                      onChange={(e) =>
                                        void run(async () => {
                                          await collaborationApi.setSharedTripMemberRole(
                                            {
                                              sharedTripId: st.id,
                                              memberUserId: mid,
                                              role: e.target.value as
                                                | "editor"
                                                | "viewer",
                                            },
                                          );
                                          await loadOverview();
                                          setNotice(
                                            `已更新 ${friendLabel(m.user)} 的成员权限`,
                                          );
                                        })
                                      }
                                    >
                                      <option value="editor">共同编辑</option>
                                      <option value="viewer">只读成员</option>
                                    </select>
                                    <Button
                                      variant="ghost"
                                      size="sm"
                                      disabled={busy}
                                      onClick={() =>
                                        void run(async () => {
                                          await collaborationApi.removeSharedTripMember(
                                            {
                                              sharedTripId: st.id,
                                              memberUserId: mid,
                                            },
                                          );
                                          await loadOverview();
                                          setNotice(
                                            `已将 ${friendLabel(m.user)} 移出共同旅行`,
                                          );
                                        })
                                      }
                                    >
                                      移除成员
                                    </Button>
                                  </div>
                                )}
                              </li>
                            );
                          })}
                        </ul>
                      </div>

                      <div
                        className="collab-item-actions"
                        style={{ marginTop: 10 }}
                      >
                        {isOwner ? (
                          confirmDissolveId === st.id ? (
                            <>
                              <span className="collab-hint">
                                解散后协作关系结束，已加入成员各自地图中的旅行记录与城市足迹会保留为独立个人记录。
                              </span>
                              <Button
                                variant="destructive"
                                size="sm"
                                disabled={busy}
                                onClick={() =>
                                  void run(async () => {
                                    await collaborationApi.deleteSharedTrip(
                                      st.id,
                                    );
                                    setConfirmDissolveId(null);
                                    await loadOverview();
                                    setNotice("已解散共同旅行");
                                  })
                                }
                              >
                                确认解散共同旅行
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={busy}
                                onClick={() => setConfirmDissolveId(null)}
                              >
                                取消
                              </Button>
                            </>
                          ) : (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={busy}
                              onClick={() => setConfirmDissolveId(st.id)}
                            >
                              <Trash size={15} />
                              解散共同旅行
                            </Button>
                          )
                        ) : confirmLeaveId === st.id ? (
                          <>
                            <span className="collab-hint">
                              退出后仅移除本次共同旅行记录，你在该城市的打卡状态、其他旅行及他人的记录均会保留。
                            </span>
                            <Button
                              variant="destructive"
                              size="sm"
                              disabled={busy}
                              onClick={() =>
                                void run(async () => {
                                  await collaborationApi.leaveSharedTrip(st.id);
                                  setConfirmLeaveId(null);
                                  void cloud.retry();
                                  await loadOverview();
                                  setNotice(
                                    `已退出 ${st.regionName} 的共同旅行，你的城市打卡与其他记录已保留`,
                                  );
                                })
                              }
                            >
                              确认退出共同旅行
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={busy}
                              onClick={() => setConfirmLeaveId(null)}
                            >
                              取消
                            </Button>
                          </>
                        ) : (
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={busy}
                            onClick={() => setConfirmLeaveId(st.id)}
                          >
                            退出共同旅行
                          </Button>
                        )}
                      </div>
                    </div>
                  );
                })
              )}
            </section>
          </div>
        )}

        <div className="dialog-actions">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
