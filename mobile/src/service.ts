export type Door = {
  id: string;
  name: string;
  community: string;
  available: boolean;
};

export type Share = {
  id: string;
  name: string;
  doorId: string;
  enabled: boolean;
};

export type Account = {
  id: string;
  maskedPhone: string;
  authorizedAt: string;
  authorization: "valid" | "expired";
  widgetDoorId: string | null;
};

export type Snapshot = {
  account: Account;
  doors: Door[];
  shares: Share[];
};

export type SmsIdentity = { kind: "login"; phone: string } | { kind: "renew" };
export type OpenResult = { state: "accepted" | "pending" | "rejected" | "unknown"; message: string };
export type ShareStatus = "ready" | "paused" | "missingDoor" | "expired";
export type IssuedShare = { share: Share; url: string; credential: string };

export interface MobileService {
  current(): Promise<Snapshot | null>;
  sendSms(identity: SmsIdentity): Promise<void>;
  verifySms(identity: SmsIdentity, code: string): Promise<Snapshot>;
  signOut(): Promise<void>;
  open(doorId: string, requestId: string): Promise<OpenResult>;
  createShare(name: string, doorId: string): Promise<{ snapshot: Snapshot; issued: IssuedShare }>;
  setShareEnabled(id: string, enabled: boolean): Promise<Snapshot>;
  deleteShare(id: string): Promise<Snapshot>;
  setWidgetDoor(doorId: string): Promise<Snapshot>;
}

export function getShareStatus(snapshot: Snapshot, share: Share): ShareStatus {
  if (!snapshot.doors.some((door) => door.id === share.doorId && door.available)) return "missingDoor";
  if (!share.enabled) return "paused";
  return snapshot.account.authorization === "valid" ? "ready" : "expired";
}

export const isPhone = (phone: string) => /^1[0-9]{10}$/.test(phone);
export const isSmsCode = (code: string) => /^[0-9]{6}$/.test(code);

const formatDate = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
  hour: "2-digit", minute: "2-digit", hourCycle: "h23",
});

export function authorizationTime(value: string) {
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? formatDate.format(timestamp) : "—";
}

export function authorizationAge(value: string, now: number) {
  const minutes = Math.max(0, Math.floor((now - Date.parse(value)) / 60_000));
  if (!Number.isFinite(minutes)) return "—";
  if (minutes === 0) return "不足 1 分钟";
  const groups = [
    [Math.floor(minutes / 1440), "天"],
    [Math.floor(minutes % 1440 / 60), "小时"],
    [minutes % 60, "分钟"],
  ] as const;
  return groups.filter(([count]) => count > 0).map(([count, unit]) => count + " " + unit).join(" ");
}
