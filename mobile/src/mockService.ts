import AsyncStorage from "@react-native-async-storage/async-storage";
import { CryptoDigestAlgorithm, digestStringAsync, randomUUID } from "expo-crypto";
import { isPhone, isSmsCode, type MobileService, type SmsIdentity, type Snapshot } from "./service";

const storageKey = "qinlin:mobile:demo:v1";
const demoCode = "123456";
const community = "松庭花园";
type Store = { active: string | null; byAccount: Record<string, Snapshot> };

let storePromise: Promise<Store> | undefined;
let challenge: { accountId: string; kind: SmsIdentity["kind"]; sentAt: number } | undefined;

function readStore(): Promise<Store> {
  storePromise ??= AsyncStorage.getItem(storageKey).then((raw) =>
    raw ? JSON.parse(raw) as Store : { active: null, byAccount: {} },
  ).catch((error: unknown) => {
    storePromise = undefined;
    throw error;
  });
  return storePromise;
}

async function writeStore(store: Store) {
  await AsyncStorage.setItem(storageKey, JSON.stringify(store));
  storePromise = Promise.resolve(store);
}

async function requireAccount() {
  const store = await readStore();
  const snapshot = store.active ? store.byAccount[store.active] : undefined;
  if (!snapshot) throw new Error("请先登录");
  return snapshot;
}

async function saveSnapshot(snapshot: Snapshot) {
  const store = await readStore();
  const id = snapshot.account.id;
  await writeStore({ active: id, byAccount: { ...store.byAccount, [id]: snapshot } });
  return snapshot;
}

async function identityKey(identity: SmsIdentity) {
  if (identity.kind === "renew") return (await requireAccount()).account.id;
  if (!isPhone(identity.phone)) throw new Error("请输入有效的 11 位手机号");
  return digestStringAsync(CryptoDigestAlgorithm.SHA256, identity.phone);
}

function sampleSnapshot(id: string, phone: string, authorizedAt: string): Snapshot {
  return {
    account: { id, maskedPhone: phone.slice(0, 3) + "••••" + phone.slice(-4),
      authorizedAt, authorization: "valid", widgetDoorId: "south" },
    doors: [
      { id: "south", name: "南门", community, available: true },
      { id: "north", name: "北门", community, available: true },
      { id: "garage", name: "地库入口", community, available: false },
    ],
    shares: [
      { id: id + "-family", name: "家人", doorId: "south", enabled: true },
      { id: id + "-guest", name: "临时来访", doorId: "north", enabled: false },
      { id: id + "-parking", name: "停车", doorId: "garage", enabled: true },
    ],
  };
}

function allowedDoor(snapshot: Snapshot, doorId: string) {
  return snapshot.doors.find((door) => door.id === doorId && door.available);
}

function requireShare(snapshot: Snapshot, id: string) {
  const share = snapshot.shares.find((row) => row.id === id);
  if (!share) throw new Error("接口已删除或不可用");
  return share;
}

export const mockService: MobileService = {
  async current() {
    const store = await readStore();
    return store.active ? store.byAccount[store.active] ?? null : null;
  },
  async sendSms(identity) {
    const accountId = await identityKey(identity);
    challenge = { accountId, kind: identity.kind, sentAt: Date.now() };
  },
  async verifySms(identity, code) {
    const accountId = await identityKey(identity);
    if (!challenge || challenge.accountId !== accountId || challenge.kind !== identity.kind ||
      Date.now() - challenge.sentAt > 5 * 60_000) {
      throw new Error("请先获取新的验证码");
    }
    if (!isSmsCode(code) || code !== demoCode) throw new Error("验证码有误");
    const { byAccount } = await readStore();
    const previous = byAccount[accountId];
    const authorizedAt = new Date().toISOString();
    if (previous) {
      const snapshot = await saveSnapshot({
        ...previous, account: { ...previous.account, authorizedAt, authorization: "valid" },
      });
      challenge = undefined;
      return snapshot;
    }
    if (identity.kind !== "login") throw new Error("请重新登录");
    const snapshot = await saveSnapshot(sampleSnapshot(accountId, identity.phone, authorizedAt));
    challenge = undefined;
    return snapshot;
  },
  async signOut() {
    const store = await readStore();
    await writeStore({ ...store, active: null });
    challenge = undefined;
  },
  async open(doorId, _requestId) {
    const snapshot = await requireAccount();
    if (snapshot.account.authorization === "expired") return { state: "rejected", message: "请先重新授权" };
    if (!allowedDoor(snapshot, doorId)) return { state: "rejected", message: "该门禁当前不可用" };
    return { state: "accepted", message: "模拟请求已受理" };
  },
  async createShare(name, doorId) {
    const snapshot = await requireAccount();
    if (snapshot.account.authorization !== "valid" || !allowedDoor(snapshot, doorId)) {
      throw new Error("请选择当前账号有权限且已启用的门禁");
    }
    const label = name.trim();
    if (!label || label.length > 24) throw new Error("请输入 1–24 个字的接口名称");
    const share = { id: randomUUID(), name: label, doorId, enabled: true };
    return {
      snapshot: await saveSnapshot({ ...snapshot, shares: [...snapshot.shares, share] }),
      issued: {
        share, url: "https://qinlin.example/open/" + share.id, credential: "demo_" + randomUUID(),
      },
    };
  },
  async setShareEnabled(id, enabled) {
    const snapshot = await requireAccount();
    const share = requireShare(snapshot, id);
    if (enabled && !allowedDoor(snapshot, share.doorId)) throw new Error("当前没有这扇门的权限");
    return saveSnapshot({
      ...snapshot, shares: snapshot.shares.map((row) => row.id === id ? { ...row, enabled } : row),
    });
  },
  async deleteShare(id) {
    const snapshot = await requireAccount();
    requireShare(snapshot, id);
    return saveSnapshot({ ...snapshot, shares: snapshot.shares.filter((row) => row.id !== id) });
  },
  async setWidgetDoor(doorId) {
    const snapshot = await requireAccount();
    if (!allowedDoor(snapshot, doorId)) throw new Error("该门禁当前不可用");
    return saveSnapshot({ ...snapshot, account: { ...snapshot.account, widgetDoorId: doorId } });
  },
};
