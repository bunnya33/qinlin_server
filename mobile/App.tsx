import { useEffect, useRef, useState, type ReactNode } from "react";
import { AppState, Image, KeyboardAvoidingView, Modal, Platform, Pressable, ScrollView, Share as SystemShare,
  StyleSheet, Text, TextInput, View } from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { StatusBar } from "expo-status-bar";
import * as Clipboard from "expo-clipboard";
import { randomUUID } from "expo-crypto";
import { ArrowLeft, Check, CircleCheck, Copy, DoorClosed, DoorOpen, Link2, MapPin, PanelsTopLeft, Pause, Play,
  Plus, RefreshCw, Share2, Trash2, UserRound } from "lucide-react-native";
import { mockService as service } from "./src/mockService";
import { authorizationAge, authorizationTime, getShareStatus, isPhone, isSmsCode, type IssuedShare,
  type OpenResult, type Share, type SmsIdentity, type Snapshot } from "./src/service";
import { Badge, Button, color, IconTile, TabBar, type Tab } from "./src/ui";

const gate = require("./assets/gate.jpg");
const samplePhone = "13800132608";
type Detail = "renew" | "newShare" | "issued" | "widget";

function Header({ title, subtitle, action }: { title: string; subtitle?: string; action?: ReactNode }) {
  return <View style={s.header}>
    <View style={s.flex}><Text accessibilityRole="header" style={s.title}>{title}</Text>
      {subtitle && <Text style={s.secondary}>{subtitle}</Text>}
    </View>{action}
  </View>;
}

function Notice({ children, error = false, success = false }: { children: string; error?: boolean; success?: boolean }) {
  return <Text accessibilityRole={error ? "alert" : undefined} accessibilityLiveRegion="polite"
    style={[s.notice, success && s.noticeSuccess, error && s.error]}>{children}</Text>;
}

function AuthorizationMetrics({ at, now }: { at: string; now: number }) {
  return <View style={s.metrics}>
    <View style={s.flex}><Text style={s.labelSmall}>上次授权</Text><Text style={s.metricValue}>{authorizationTime(at)}</Text></View>
    <View style={[s.flex, s.metricRight]}><Text style={s.labelSmall}>距上次授权</Text>
      <Text style={s.metricValue}>{authorizationAge(at, now)}</Text></View>
  </View>;
}

function useClock() {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const tick = () => setNow(Date.now());
    const timer = setInterval(tick, 1_000);
    const sub = AppState.addEventListener("change", (state) => { if (state === "active") tick(); });
    return () => { clearInterval(timer); sub.remove(); };
  }, []);
  return now;
}

function SmsForm({ snapshot, onVerified }: { snapshot?: Snapshot; onVerified: (value: Snapshot) => void }) {
  const [phone, setPhone] = useState(samplePhone);
  const [code, setCode] = useState("");
  const [sentFor, setSentFor] = useState("");
  const [resendAfter, setResendAfter] = useState(0);
  const [feedback, setFeedback] = useState("");
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const now = useClock();
  const recipient = snapshot?.account.id ?? phone;
  const identity: SmsIdentity = snapshot ? { kind: "renew" } : { kind: "login", phone };
  const seconds = Math.max(0, Math.ceil((resendAfter - now) / 1_000));
  const valid = Boolean(snapshot) || isPhone(phone);

  async function run(work: () => Promise<void>) {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setFeedback("");
    setError(false);
    try { await work(); }
    catch (cause) { setError(true); setFeedback(cause instanceof Error ? cause.message : "操作失败，请稍后重试"); }
    finally { running.current = false; setBusy(false); }
  }

  return <>
    {!snapshot && <Image source={gate} style={s.loginImage} resizeMode="cover" accessible={false} />}
    <Header title={snapshot ? "重新授权" : "手机号登录"} subtitle={snapshot ? "使用原手机号验证亲邻门禁权限" : "亲邻开门 · 本地演示"} />
    {snapshot && <AuthorizationMetrics at={snapshot.account.authorizedAt} now={now} />}
    <Text style={s.label}>手机号</Text>
    {snapshot ? <Text style={s.readonly}>{snapshot.account.maskedPhone}</Text> :
      <View style={s.phoneInput}>
        <Text style={s.prefix}>+86</Text>
        <TextInput accessibilityLabel="手机号" style={s.inputInner} keyboardType="phone-pad" autoComplete="tel-national"
          value={phone} onChangeText={(text) => { setPhone(text.replace(/\D/g, "")); setCode(""); }} maxLength={11}
          placeholder="输入手机号" placeholderTextColor={color.muted} />
      </View>}
    <Text style={s.label}>短信验证码</Text>
    <View style={s.phoneInput}>
      <TextInput accessibilityLabel="短信验证码" style={s.inputInner} keyboardType="number-pad" autoComplete="one-time-code"
        textContentType="oneTimeCode" value={code} onChangeText={(text) => setCode(text.replace(/\D/g, ""))}
        placeholder="输入验证码" placeholderTextColor={color.muted} maxLength={6} />
      <Button title={seconds > 0 ? seconds + " 秒后重发" : "获取验证码"} disabled={!valid || busy || seconds > 0} tone="tonal"
        onPress={() => void run(async () => {
          await service.sendSms(identity);
          setResendAfter(Date.now() + 60_000);
          setSentFor(recipient);
          setFeedback("本地演示验证码为 123456");
        })} />
    </View>
    {feedback ? <Notice error={error}>{feedback}</Notice> : null}
    {snapshot && <Notice>同账号续授权后，仍有门权限且已启用的接口沿用原地址与凭据。手动暂停的接口保持暂停。</Notice>}
    <Button title={snapshot ? "确认续授权" : "登录"} tone="primary" disabled={!valid || !isSmsCode(code) || busy || sentFor !== recipient}
      onPress={() => void run(async () => onVerified(await service.verifySms(identity, code)))} />
  </>;
}

function DoorPicker({ snapshot, selected, onSelect }: {
  snapshot: Snapshot; selected: string; onSelect: (id: string) => void;
}) {
  return <View style={s.card}>
    {snapshot.doors.filter((door) => door.available).map((door) =>
      <Pressable key={door.id} accessibilityRole="radio" accessibilityState={{ checked: selected === door.id }}
        style={s.pickerRow} onPress={() => onSelect(door.id)}>
        <DoorClosed size={18} strokeWidth={1.75} color={color.purple} />
        <Text style={[s.body, s.flex]}>{door.name}</Text>
        {selected === door.id && <Check size={18} strokeWidth={2} color={color.purple} />}
      </Pressable>,
    )}
  </View>;
}

const shareStatus = {
  ready: { text: "可用", tone: "good" }, paused: { text: "已暂停", tone: "neutral" },
  missingDoor: { text: "门无权限", tone: "error" }, expired: { text: "待重新授权", tone: "warn" },
} as const;

function Home() {
  const [ready, setReady] = useState(false);
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [tab, setTab] = useState<Tab>("doors");
  const [detail, setDetail] = useState<Detail | null>(null);
  const [issued, setIssued] = useState<IssuedShare>();
  const [name, setName] = useState("");
  const [doorId, setDoorId] = useState("");
  const [remove, setRemove] = useState<Share>();
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<{ message: string; error?: boolean }>();
  const [result, setResult] = useState<OpenResult>();
  const running = useRef(false);
  const now = useClock();
  const page = useRef<ScrollView>(null);

  async function run(work: () => Promise<void>) {
    if (running.current) return;
    running.current = true;
    setFeedback(undefined);
    setBusy(true);
    try { await work(); }
    catch (cause) { setFeedback({ message: cause instanceof Error ? cause.message : "操作失败，请稍后重试", error: true }); }
    finally { running.current = false; setBusy(false); }
  }

  async function load() {
    setSnapshot(await service.current());
    setReady(true);
  }
  useEffect(() => {
    void load().catch(() => setFeedback({ message: "读取本地数据失败", error: true }));
  }, []);

  function navigate(next: Detail | null) {
    setFeedback(undefined);
    if (next !== "issued") setIssued(undefined);
    setDetail(next);
    page.current?.scrollTo({ y: 0, animated: false });
  }

  function changeTab(next: Tab) {
    if (running.current || tab === next) return;
    setFeedback(undefined);
    setTab(next);
    page.current?.scrollTo({ y: 0, animated: false });
  }

  const availableDoors = snapshot?.doors.filter((door) => door.available) ?? [];
  const favorite = availableDoors[0];
  const authValid = snapshot?.account.authorization === "valid";
  const site = favorite?.community;

  function open(id: string) {
    void run(async () => {
      setResult({ state: "pending", message: "正在提交开门请求" });
      try { setResult(await service.open(id, randomUUID())); }
      catch { setResult({ state: "unknown", message: "结果未确认，请先查看门禁状态" }); }
    });
  }

  async function copy(text: string) {
    const copied = await Clipboard.setStringAsync(text);
    if (!copied) throw new Error("无法写入剪贴板");
    setFeedback({ message: "已复制" });
  }

  function startPicker(screen: "newShare" | "widget") {
    setDoorId((screen === "widget" ? snapshot?.account.widgetDoorId : favorite?.id) ?? "");
    setName("");
    navigate(screen);
  }

  let content: ReactNode;
  if (!ready) {
    content = <><Header title="亲邻开门" />{feedback ? <Button title="重试" onPress={() => void run(load)} /> :
      <Text style={s.secondary}>正在读取账号…</Text>}</>;
  } else if (!snapshot || detail === "renew") {
    content = <SmsForm key={snapshot ? "renew" : "login"} snapshot={snapshot ?? undefined} onVerified={(value) => {
      setSnapshot(value);
      setResult(undefined);
      if (snapshot) setTab("profile");
      navigate(null);
    }} />;
  } else if (detail === "widget" || detail === "newShare") {
    const widget = detail === "widget";
    const selectedAvailable = availableDoors.some((door) => door.id === doorId);
    content = <>
      <Header title={widget ? "小组件用门" : "新建开门接口"}
        subtitle={widget ? "选择小组件默认显示的门禁" : "接口绑定当前账号和一扇门"} />
      {!widget && <>
        <Text style={s.label}>所属账号</Text><Text style={s.readonly}>{snapshot.account.maskedPhone}</Text>
        <Text style={s.label}>接口名称</Text>
        <TextInput style={s.input} accessibilityLabel="接口名称" value={name} onChangeText={setName} maxLength={24}
          placeholder="例如 家人" placeholderTextColor={color.muted} />
      </>}
      <Text style={s.label}>{widget ? "可选门禁" : "绑定门禁"}</Text>
      {availableDoors.length > 0 ? <DoorPicker snapshot={snapshot} selected={doorId} onSelect={setDoorId} /> :
        <Notice>当前没有可用门禁</Notice>}
      {widget && <Notice>当前保存小组件的门禁选择。iPhone 小组件将在原生扩展接入后显示。</Notice>}
      <Button title={widget ? "保存" : "生成接口"} tone="primary"
        disabled={busy || !selectedAvailable || (!widget && (!name.trim() || !authValid))}
        onPress={() => void run(async () => {
          if (widget) {
            setSnapshot(await service.setWidgetDoor(doorId));
            navigate(null);
          } else {
            const value = await service.createShare(name, doorId);
            setSnapshot(value.snapshot);
            setIssued(value.issued);
            navigate("issued");
          }
        })} />
    </>;
  } else if (detail === "issued" && issued) {
    const doorName = snapshot.doors.find((door) => door.id === issued.share.doorId)?.name;
    content = <>
      <View style={[s.card, s.success]}><IconTile icon={CircleCheck} background={color.card} foreground={color.forest} />
        <Header title="接口已生成" subtitle={issued.share.name + " · " + (doorName ?? "")} />
      </View>
      <View style={[s.card, s.credential]}>
        <Text style={s.label}>接口地址</Text>
        <Text style={s.secondary} selectable>{issued.url}</Text>
        <Button title="复制地址" icon={Copy} disabled={busy} onPress={() => void run(() => copy(issued.url))} />
        <Text style={s.label}>调用凭据</Text>
        <Text style={s.secondary}>••••••••••••••••</Text>
        <Button title="复制凭据" icon={Copy} disabled={busy} onPress={() => void run(() => copy(issued.credential))} />
      </View>
      <Button title="分享接口" icon={Share2} tone="primary" disabled={busy} onPress={() => void run(async () => {
        const message = issued.url + "\n调用凭据 " + issued.credential;
        if (Platform.OS === "web") await copy(message);
        else await SystemShare.share({ message });
      })} />
    </>;
  } else if (tab === "shares") {
    content = <>
      <Header title="我的分享" action={<Button title="新建" icon={Plus} tone="primary"
        disabled={busy || !authValid || !favorite} onPress={() => startPicker("newShare")} />} />
      {snapshot.shares.length === 0 && <Notice>还没有分享接口</Notice>}
      {snapshot.shares.map((share) => {
        const door = snapshot.doors.find((row) => row.id === share.doorId);
        const state = getShareStatus(snapshot, share);
        return <View key={share.id} style={s.card}>
          <View style={s.row}>
            <IconTile icon={Link2} />
            <View style={s.flex}><Text style={s.cardTitle}>{share.name}</Text>
              <Text style={s.secondary}>{door ? door.community + " · " + door.name : "门禁已移除"}</Text></View>
            <Badge {...shareStatus[state]} />
          </View>
          <View style={s.actions}>
            {state !== "missingDoor" && <Button title={share.enabled ? "暂停" : "启用"}
              icon={share.enabled ? Pause : Play} tone={share.enabled ? "normal" : "tonal"} disabled={busy}
              onPress={() => void run(async () => setSnapshot(await service.setShareEnabled(share.id, !share.enabled)))} />}
            <Button title="删除" icon={Trash2} tone="danger" disabled={busy} onPress={() => setRemove(share)} />
          </View>
        </View>;
      })}
    </>;
  } else if (tab === "profile") {
    content = <>
      <Header title="我的" />
      <View style={s.card}>
        <View style={s.row}><IconTile icon={UserRound} /><View style={s.flex}><Text style={s.cardTitle}>亲邻账号</Text>
          <Text style={s.secondary}>{snapshot.account.maskedPhone}</Text></View>
          <Badge text={authValid ? "授权有效" : "需重新授权"} tone={authValid ? "good" : "warn"} />
        </View>
        <AuthorizationMetrics at={snapshot.account.authorizedAt} now={now} />
        <Button title="重新授权" tone="tonal" icon={RefreshCw} onPress={() => navigate("renew")} disabled={busy} />
      </View>
      <Button title="退出本地账号" disabled={busy} onPress={() => void run(async () => {
        await service.signOut();
        setSnapshot(null);
        setTab("doors");
        setResult(undefined);
        navigate(null);
      })} />
    </>;
  } else {
    content = <>
      <Header title="我的门禁" subtitle={site} action={<Badge text="模拟模式" tone="neutral" />} />
      {!authValid && <Button title="重新授权" icon={RefreshCw} tone="tonal" onPress={() => navigate("renew")} disabled={busy} />}
      {favorite ? <View style={s.hero}>
        <Image source={gate} style={s.heroArt} resizeMode="cover" accessible={false} />
        <Text style={s.secondary}>常用门禁</Text>
        <Text style={s.heroName}>{favorite.name}</Text>
        <View style={s.heroButton}><Button title="开门" icon={DoorOpen} tone="primary"
          disabled={busy || !authValid} onPress={() => open(favorite.id)} /></View>
      </View> : <Notice>当前没有可用门禁</Notice>}
      {result && <Notice success={result.state === "accepted"}
        error={result.state === "rejected" || result.state === "unknown"}>{result.message}</Notice>}
      {availableDoors.length > 1 && <Text style={s.cardTitle}>其他门禁</Text>}
      {availableDoors.slice(1).map((door) => <View key={door.id} style={[s.card, s.row]}>
        <IconTile icon={DoorClosed} background={color.peach} foreground={color.brown} />
        <View style={s.flex}><Text style={s.cardTitle}>{door.name}</Text>
          <View style={s.row}><MapPin size={14} color={color.muted} /><Text style={s.secondary}>{door.community}</Text></View>
        </View>
        <Button title="开门" disabled={busy || !authValid} onPress={() => open(door.id)} />
      </View>)}
      {favorite && <Button title={"小组件用门 · " +
        (availableDoors.find((door) => door.id === snapshot.account.widgetDoorId)?.name ?? "未设置")}
        tone="tonal" icon={PanelsTopLeft} disabled={busy} onPress={() => startPicker("widget")} />}
    </>;
  }

  return <SafeAreaView style={s.screen}>
    <StatusBar style="dark" />
    <KeyboardAvoidingView style={s.outer} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <ScrollView ref={page} contentContainerStyle={s.scroll} keyboardShouldPersistTaps="handled">
        {detail && <View style={s.back}><Button title="返回" icon={ArrowLeft} disabled={busy} onPress={() => navigate(null)} /></View>}
        {content}
        {feedback && <Notice error={feedback.error}>{feedback.message}</Notice>}
      </ScrollView>
      {snapshot && !detail && <TabBar active={tab} onChange={changeTab} />}
    </KeyboardAvoidingView>
    <Modal transparent visible={Boolean(remove)} animationType="fade" onRequestClose={() => !busy && setRemove(undefined)}>
      <View style={s.scrim}>
        <View style={s.dialog} accessibilityViewIsModal>
          <Text accessibilityRole="header" style={s.cardTitle}>删除「{remove?.name}」？</Text>
          <Text style={s.secondary}>原地址和凭据将永久失效，所有持有者均无法继续使用。</Text>
          <View style={s.actions}>
            <Button title="取消" disabled={busy} onPress={() => setRemove(undefined)} />
            <Button title="永久删除" tone="destructive" disabled={busy} onPress={() => {
              if (!remove) return;
              const id = remove.id;
              void run(async () => setSnapshot(await service.deleteShare(id))).finally(() => setRemove(undefined));
            }} />
          </View>
        </View>
      </View>
    </Modal>
  </SafeAreaView>;
}

export default function App() {
  return <SafeAreaProvider><Home /></SafeAreaProvider>;
}

const s = StyleSheet.create({
  screen: { flex: 1, backgroundColor: color.page },
  outer: { flex: 1, width: "100%", maxWidth: 480, alignSelf: "center" },
  scroll: { paddingHorizontal: 20, paddingTop: 10, paddingBottom: 24, gap: 12 },
  header: { gap: 8, flexDirection: "row", alignItems: "center", marginBottom: 4 },
  title: { fontSize: 24, lineHeight: 32, fontWeight: "600", color: color.ink },
  secondary: { fontSize: 14, lineHeight: 21, color: color.muted },
  body: { fontSize: 16, lineHeight: 24, color: color.ink },
  cardTitle: { fontSize: 17, lineHeight: 25, fontWeight: "600", color: color.ink },
  card: { backgroundColor: color.card, borderColor: color.line, borderWidth: 1, borderRadius: 20, padding: 14, gap: 11 },
  row: { flexDirection: "row", alignItems: "center", gap: 9 },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  flex: { flex: 1 },
  back: { alignSelf: "flex-start" },
  hero: { borderRadius: 20, overflow: "hidden", backgroundColor: color.lavender, minHeight: 169,
    padding: 15, gap: 4, justifyContent: "center" },
  heroArt: { position: "absolute", top: 0, left: 0, width: "100%", height: "100%" },
  heroName: { fontSize: 21, lineHeight: 29, fontWeight: "600", color: color.ink },
  heroButton: { alignSelf: "flex-start", minWidth: 116, marginTop: 7 },
  loginImage: { width: "100%", height: 147, borderRadius: 23, marginBottom: 6 },
  notice: { padding: 11, borderRadius: 13, backgroundColor: color.lavender, color: color.purple, fontSize: 13, lineHeight: 20 },
  noticeSuccess: { backgroundColor: color.green, color: color.forest },
  error: { backgroundColor: color.blush, color: color.red },
  label: { fontSize: 14, lineHeight: 20, fontWeight: "600", color: color.ink },
  labelSmall: { fontSize: 12, lineHeight: 18, color: color.muted },
  input: { minHeight: 48, backgroundColor: color.card, color: color.ink, borderColor: color.line, borderWidth: 1,
    borderRadius: 14, paddingHorizontal: 12, fontSize: 16 },
  phoneInput: { minHeight: 50, backgroundColor: color.card, borderColor: color.line, borderWidth: 1, borderRadius: 14,
    paddingHorizontal: 8, paddingVertical: 3, flexDirection: "row", gap: 6, alignItems: "center" },
  inputInner: { flex: 1, paddingHorizontal: 4, minWidth: 0, minHeight: 44, fontSize: 16, color: color.ink },
  prefix: { paddingHorizontal: 5, color: color.muted, fontSize: 14 },
  readonly: { minHeight: 45, padding: 11, borderRadius: 13, backgroundColor: color.gray, color: color.ink, fontSize: 15 },
  metrics: { flexDirection: "row", gap: 9, backgroundColor: "#F5F4F8", padding: 10, borderRadius: 14 },
  metricRight: { borderLeftColor: color.line, borderLeftWidth: 1, paddingLeft: 8 },
  metricValue: { color: color.ink, fontSize: 14, fontWeight: "600", lineHeight: 21, fontVariant: ["tabular-nums"] },
  pickerRow: { minHeight: 44, flexDirection: "row", alignItems: "center", gap: 9, paddingHorizontal: 4 },
  success: { backgroundColor: color.green, borderWidth: 0, gap: 6 },
  credential: { backgroundColor: "#EEEDF8", borderWidth: 0, gap: 8 },
  scrim: { flex: 1, backgroundColor: "#24252A66", justifyContent: "center", alignItems: "center", padding: 20 },
  dialog: { width: "100%", maxWidth: 334, padding: 16, gap: 12, borderRadius: 20, backgroundColor: color.card },
});
