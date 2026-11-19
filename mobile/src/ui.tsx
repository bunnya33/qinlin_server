import { useEffect, useRef, useState } from "react";
import { AccessibilityInfo, Animated, Easing, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { DoorClosed, Link2, UserRound, type LucideIcon } from "lucide-react-native";

export const color = {
  page: "#FAF9F6", card: "#FFFFFF", line: "#E7E5EA", ink: "#24252A", muted: "#626572",
  lavender: "#E5E6FA", purple: "#464579", peach: "#FFE7CF", brown: "#784418",
  green: "#E6F4EB", forest: "#255E43", gray: "#F0F0F3", red: "#AE303B", blush: "#FCECEF",
};

const tones = {
  normal: [color.card, color.ink, color.line],
  primary: [color.ink, color.card, color.ink],
  tonal: [color.lavender, color.purple, color.lavender],
  danger: [color.card, color.red, color.line],
  destructive: [color.red, color.card, color.red],
} as const;

export function Button({ title, icon: Icon, onPress, tone = "normal", disabled = false }: {
  title: string;
  icon?: LucideIcon;
  onPress: () => void;
  tone?: keyof typeof tones;
  disabled?: boolean;
}) {
  const [backgroundColor, foreground, borderColor] = tones[tone];
  return (
    <Pressable accessibilityRole="button" accessibilityState={{ disabled }} disabled={disabled} onPress={onPress}
      style={({ pressed }) => [styles.button, { backgroundColor, borderColor, opacity: disabled ? 0.48 : pressed ? 0.75 : 1 }]}>
      {Icon && <Icon size={18} strokeWidth={1.75} color={foreground} />}
      <Text style={[styles.buttonText, { color: foreground }]}>{title}</Text>
    </Pressable>
  );
}

export function IconTile({ icon: Icon, background = color.lavender, foreground = color.purple }: {
  icon: LucideIcon; background?: string; foreground?: string;
}) {
  return <View style={[styles.tile, { backgroundColor: background }]}><Icon size={20} strokeWidth={1.75} color={foreground} /></View>;
}

export function Badge({ text, tone = "good" }: { text: string; tone?: "good" | "warn" | "neutral" | "error" }) {
  const [bg, fg] = {
    good: [color.green, color.forest], warn: [color.peach, color.brown],
    neutral: [color.gray, color.muted], error: [color.blush, color.red],
  }[tone];
  return <View style={[styles.badge, { backgroundColor: bg }]}>
    <View style={[styles.dot, { backgroundColor: fg }]} />
    <Text style={[styles.badgeText, { color: fg }]}>{text}</Text>
  </View>;
}

const tabs = [
  { id: "doors", title: "门禁", icon: DoorClosed },
  { id: "shares", title: "分享", icon: Link2 },
  { id: "profile", title: "我的", icon: UserRound },
] as const;
export type Tab = typeof tabs[number]["id"];
const tabWidth = 70;

export function TabBar({ active, onChange }: { active: Tab; onChange: (tab: Tab) => void }) {
  const index = tabs.findIndex((tab) => tab.id === active);
  const offset = useRef(new Animated.Value(index * tabWidth)).current;
  const [reduceMotion, setReduceMotion] = useState(false);
  useEffect(() => {
    let mounted = true;
    void AccessibilityInfo.isReduceMotionEnabled().then((enabled) => {
      if (mounted) setReduceMotion(enabled);
    });
    const listener = AccessibilityInfo.addEventListener("reduceMotionChanged", setReduceMotion);
    return () => { mounted = false; listener.remove(); };
  }, []);
  useEffect(() => {
    if (reduceMotion) {
      offset.setValue(index * tabWidth);
      return;
    }
    const slide = Animated.timing(offset, {
      toValue: index * tabWidth, duration: 280,
      easing: Easing.bezier(0.22, 0.72, 0.28, 1), useNativeDriver: Platform.OS !== "web",
    });
    slide.start();
    return () => slide.stop();
  }, [index, offset, reduceMotion]);
  return (
    <View accessibilityRole="tablist" accessibilityLabel="手机导航" style={styles.navigation}>
      <Animated.View pointerEvents="none" style={[styles.indicator, { transform: [{ translateX: offset }] }]} />
      {tabs.map(({ id, title, icon: Icon }) => {
        const selected = id === active;
        const ink = selected ? color.purple : "#F1EFFA";
        return <Pressable key={id} style={styles.tab} accessibilityRole="tab" aria-selected={selected}
          onPress={() => onChange(id)}>
          <Icon size={20} strokeWidth={1.75} color={ink} />
          <Text style={[styles.tabLabel, { color: ink, fontWeight: selected ? "600" : "400" }]}>{title}</Text>
        </Pressable>;
      })}
    </View>
  );
}

const styles = StyleSheet.create({
  button: { minHeight: 44, paddingVertical: 10, paddingHorizontal: 13, borderWidth: 1, borderRadius: 14,
    alignItems: "center", justifyContent: "center", flexDirection: "row", gap: 7 },
  buttonText: { fontSize: 14, lineHeight: 20, fontWeight: "600", textAlign: "center" },
  tile: { width: 38, height: 38, borderRadius: 12, alignItems: "center", justifyContent: "center" },
  badge: { gap: 5, alignItems: "center", flexDirection: "row", paddingHorizontal: 8, paddingVertical: 4,
    borderRadius: 36, alignSelf: "flex-start" },
  badgeText: { fontSize: 12, lineHeight: 16, fontWeight: "600" },
  dot: { width: 5, height: 5, borderRadius: 3 },
  navigation: { width: 228, minHeight: 54, alignSelf: "center", borderRadius: 40, backgroundColor: color.ink,
    flexDirection: "row", justifyContent: "center", alignItems: "center", marginTop: 6, marginBottom: 12, padding: 5 },
  indicator: { position: "absolute", left: 9, width: tabWidth, top: 5, bottom: 5,
    borderRadius: 40, backgroundColor: color.lavender },
  tab: { width: tabWidth, minHeight: 44, paddingVertical: 3, borderRadius: 25, alignItems: "center", justifyContent: "center" },
  tabLabel: { fontSize: 11, lineHeight: 15 },
});
