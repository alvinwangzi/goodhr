/** 本文件负责超级管理员分类查看、校验和编辑云端系统 JSON 配置。 */
"use client";

import AddRoundedIcon from "@mui/icons-material/AddRounded";
import DeleteOutlineRoundedIcon from "@mui/icons-material/DeleteOutlineRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import RestartAltRoundedIcon from "@mui/icons-material/RestartAltRounded";
import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import { Alert, Box, Button, Chip, Divider, IconButton, Stack, Tab, Tabs, TextField, Typography } from "@mui/material";
import { useEffect, useMemo, useState } from "react";
import JsonEditor from "@/components/admin/JsonEditor";
import { EmptyState, PageHeader, RefreshButton, SectionPanel } from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";
import { cloudRequest } from "@/lib/admin-api";

const categories = ["全部", "AI 配置", "基础配置", "订阅支付", "本地组件", "邀请帮助"] as const;

/** LocalAgentEntry 本地程序下载版本的单条记录。 */
type LocalAgentEntry = {
  version: string;
  url_win: string;
  url_mac: string;
  sha256: string;
  note: string;
};

/** emptyEntry 返回空白的下载版本条目。 */
function emptyEntry(): LocalAgentEntry {
  return { version: "", url_win: "", url_mac: "", sha256: "", note: "" };
}

/** parseLocalAgentEntries 从草稿 JSON 中解析 local_agent 数组。 */
function parseLocalAgentEntries(draft: string): LocalAgentEntry[] {
  try {
    const parsed = JSON.parse(draft || "{}");
    const arr = Array.isArray(parsed?.local_agent) ? parsed.local_agent : [];
    return arr.map((item: any) => ({
      version: String(item?.version || ""),
      url_win: String(item?.url_win || ""),
      url_mac: String(item?.url_mac || ""),
      sha256: String(item?.sha256 || ""),
      note: String(item?.note || ""),
    }));
  } catch {
    return [];
  }
}

/** mergeLocalAgentEntries 把 local_agent 数组写回草稿 JSON 字符串。 */
function mergeLocalAgentEntries(draft: string, entries: LocalAgentEntry[]): string {
  try {
    const parsed = JSON.parse(draft || "{}");
    parsed.local_agent = entries.filter((e) => e.version.trim());
    return JSON.stringify(parsed, null, 2);
  } catch {
    return draft;
  }
}

/** SystemConfigPage 管理系统配置并按用途分类。 */
export default function SystemConfigPage() {
  const { user, notify, confirm } = useAdmin();
  const [configs, setConfigs] = useState<any[]>([]);
  const [category, setCategory] = useState<(typeof categories)[number]>("全部");
  const [activeKey, setActiveKey] = useState("");
  const [draft, setDraft] = useState("{}");
  const [original, setOriginal] = useState("{}");
  const [loading, setLoading] = useState(false);
  const [localAgentEntries, setLocalAgentEntries] = useState<LocalAgentEntry[]>([]);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [editForm, setEditForm] = useState<LocalAgentEntry>(emptyEntry());

  /** load 读取全部系统配置并保持当前选中项。 */
  async function load() {
    setLoading(true);
    try {
      const data = await cloudRequest("/api/admin/system/configs/");
      const nextConfigs = data.configs || [];
      setConfigs(nextConfigs);
      const selected = nextConfigs.find((item: any) => configKey(item) === activeKey) || nextConfigs[0];
      if (selected) selectDirect(selected);
    } catch (error) { notify(error instanceof Error ? error.message : "系统配置读取失败", "error"); }
    finally { setLoading(false); }
  }

  useEffect(() => { if (user?.role === "super_admin") void load(); }, [user]);
  const filtered = useMemo(() => configs.filter((item) => category === "全部" || configCategory(configKey(item)) === category), [configs, category]);
  const active = configs.find((item) => configKey(item) === activeKey) || null;
  const dirty = draft !== original;
  const jsonError = validateJSON(draft);

  /** selectDirect 不经确认直接切换当前配置。 */
  function selectDirect(item: any) {
    const value = prettyJSON(item.config_value ?? item.value);
    setActiveKey(configKey(item));
    setDraft(value);
    setOriginal(value);
    setLocalAgentEntries(parseLocalAgentEntries(value));
    setEditingIndex(null);
    setEditForm(emptyEntry());
  }

  /** selectConfig 在存在未保存修改时确认是否放弃。 */
  async function selectConfig(item: any) {
    if (configKey(item) === activeKey) return;
    if (dirty && !(await confirm("放弃未保存修改", "当前配置有未保存修改，确认切换到其他配置吗？"))) return;
    selectDirect(item);
  }

  /** save 校验并保存当前系统配置。 */
  async function save() {
    if (!active || jsonError) return notify(jsonError || "请选择系统配置", "warning");
    setLoading(true);
    try { const formatted = prettyJSON(draft); await cloudRequest(`/api/admin/system/configs/${encodeURIComponent(activeKey)}`, { method: "PUT", body: { config_value: formatted } }); setDraft(formatted); setOriginal(formatted); notify(`${configTitle(activeKey)}已保存`, "success"); await load(); } catch (error) { notify(error instanceof Error ? error.message : "保存失败", "error"); } finally { setLoading(false); }
  }

  /** resetDraft 恢复当前配置的服务端内容。 */
  function resetDraft() {
    setDraft(original);
    setLocalAgentEntries(parseLocalAgentEntries(original));
    setEditingIndex(null);
    setEditForm(emptyEntry());
  }

  /** syncEntriesToDraft 把表单中的版本列表同步回草稿 JSON。 */
  function syncEntriesToDraft(entries: LocalAgentEntry[]) {
    setLocalAgentEntries(entries);
    setDraft(mergeLocalAgentEntries(draft, entries));
  }

  /** startAddEntry 开始添加新版本条目。 */
  function startAddEntry() {
    setEditingIndex(-1);
    setEditForm(emptyEntry());
  }

  /** startEditEntry 开始编辑指定条目。 */
  function startEditEntry(index: number) {
    setEditingIndex(index);
    setEditForm({ ...localAgentEntries[index] });
  }

  /** cancelEdit 取消编辑。 */
  function cancelEdit() {
    setEditingIndex(null);
    setEditForm(emptyEntry());
  }

  /** saveEdit 保存当前编辑的条目。 */
  function saveEdit() {
    if (!editForm.version.trim()) return notify("版本号不能为空", "warning");
    const next = [...localAgentEntries];
    if (editingIndex === -1) {
      next.push(editForm);
    } else if (editingIndex !== null) {
      next[editingIndex] = editForm;
    }
    syncEntriesToDraft(next);
    cancelEdit();
  }

  /** deleteEntry 删除指定版本条目。 */
  async function deleteEntry(index: number) {
    const entry = localAgentEntries[index];
    if (!(await confirm("删除版本", `确认删除版本 ${entry.version || "未命名"} 的下载记录吗？`))) return;
    const next = localAgentEntries.filter((_, i) => i !== index);
    syncEntriesToDraft(next);
    if (editingIndex === index) cancelEdit();
  }

  if (user?.role !== "super_admin") return <SectionPanel><EmptyState text="只有超级管理员可以访问此页面" /></SectionPanel>;
  return <><PageHeader title="系统配置" description="配置按业务用途分组。保存前会校验 JSON，错误内容不会提交。" actions={<><RefreshButton loading={loading} onClick={() => void load()} /><Button variant="outlined" startIcon={<RestartAltRoundedIcon />} disabled={!dirty || loading} onClick={resetDraft}>撤销修改</Button><Button variant="contained" startIcon={<SaveRoundedIcon />} disabled={!active || !dirty || Boolean(jsonError) || loading} onClick={() => void save()}>保存当前配置</Button></>} />
    <Tabs value={category} onChange={(_, value) => setCategory(value)} variant="scrollable" scrollButtons="auto" sx={{ mb: 2, borderBottom: "1px solid", borderColor: "divider" }}>{categories.map((item) => <Tab key={item} value={item} label={item} />)}</Tabs>
    {configs.length ? <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", lg: "260px minmax(0, 1fr)" }, gap: 2 }}><Stack spacing={1}>{filtered.map((item) => { const key = configKey(item); return <Button key={key} color={key === activeKey ? "primary" : "secondary"} variant={key === activeKey ? "contained" : "outlined"} onClick={() => void selectConfig(item)} sx={{ display: "block", minHeight: 74, p: 1.5, borderRadius: "8px", textAlign: "left" }}><Typography sx={{ fontWeight: 760 }}>{configTitle(key)}</Typography><Typography sx={{ mt: 0.25, opacity: 0.72, fontSize: 11.5 }}>{item.description || key}</Typography></Button>; })}</Stack><SectionPanel>{active ? <><Stack direction={{ xs: "column", sm: "row" }} spacing={1.5} sx={{ mb: 2, justifyContent: "space-between", alignItems: { sm: "flex-start" } }}><Box><Typography component="h2" sx={{ fontSize: 20, fontWeight: 780 }}>{configTitle(activeKey)}</Typography><Typography sx={{ mt: 0.5, color: "text.secondary", fontSize: 13 }}>{active.description || activeKey}</Typography><Typography sx={{ mt: 0.5, color: "text.secondary", fontFamily: "monospace", fontSize: 11 }}>{activeKey}</Typography></Box><Stack direction="row" spacing={1}><Chip size="small" color={active.enabled === false ? "default" : "success"} label={active.enabled === false ? "已停用" : "已启用"} />{dirty ? <Chip size="small" color="warning" label="有未保存修改" /> : null}</Stack></Stack>{activeKey === "ai.default_prompts" ? <Alert severity="info" sx={{ mb: 2 }}>这里分别保存首次筛选、打开详情和最终复核使用的系统默认提示词。岗位模板留空时会读取这些值。</Alert> : null}{activeKey === "system.subscription_plans" ? <Alert severity="info" sx={{ mb: 2 }}>每个套餐都要保留 member_type 和 allow_auto_reply。Plus 基础版设为 false，Max 全能版设为 true；保存时后端也会再次校验。</Alert> : null}{activeKey === "system.payment_wechat" ? <Alert severity="info" sx={{ mb: 2 }}>这里保存微信支付商户参数，全部明文显示，保存后立即生效，不用重启服务。private_key_base64 和 public_key_base64 支持直接粘贴 PEM 文本；8 个字段都要填，缺一个支付就用不了。</Alert> : null}{activeKey === "system.onboarding_config" ? <LocalAgentDownloadManager entries={localAgentEntries} editingIndex={editingIndex} editForm={editForm} onAdd={startAddEntry} onEdit={startEditEntry} onDelete={deleteEntry} onSave={saveEdit} onCancel={cancelEdit} onFormChange={setEditForm} /> : null}{jsonError ? <Alert severity="error" sx={{ mb: 2 }}>{jsonError}</Alert> : null}<JsonEditor value={draft} onChange={setDraft} /></> : <EmptyState text="请选择一项配置" />}</SectionPanel></Box> : <SectionPanel><EmptyState text="暂无系统配置" /></SectionPanel>}
  </>;
}

/** configKey 返回配置记录的统一键名。 */
function configKey(item: any) { return String(item?.config_key || item?.key || ""); }

/** configCategory 根据配置键返回业务分类。 */
function configCategory(key: string): (typeof categories)[number] {
  if (key.startsWith("ai.") || key.includes("prompt")) return "AI 配置";
  if (key.includes("subscription") || key.includes("payment")) return "订阅支付";
  if (key.includes("onboarding") || key.includes("runtime") || key.includes("agent")) return "本地组件";
  if (key.includes("invite") || key.includes("guide") || key.includes("help")) return "邀请帮助";
  return "基础配置";
}

/** configTitle 返回系统配置的中文标题。 */
function configTitle(key: string) { return ({ "ai.default_prompts": "AI 默认提示词", "system.app_config": "公共系统配置", "system.subscription_plans": "订阅套餐", "system.onboarding_config": "本地程序与组件", "system.invite_config": "邀请奖励", "system.email_recovery": "自动挽回邮件", "system.guide": "帮助中心", "system.payment_wechat": "微信支付配置" } as Record<string, string>)[key] || key; }

/** prettyJSON 将任意配置值格式化为缩进 JSON。 */
function prettyJSON(value: unknown) { try { const parsed = typeof value === "string" ? JSON.parse(value) : value; return JSON.stringify(parsed ?? {}, null, 2); } catch { return String(value || ""); } }

/** validateJSON 校验 JSON 文本并返回中文错误。 */
function validateJSON(value: string) { try { JSON.parse(value || "{}"); return ""; } catch (error) { return `JSON 语法错误：${error instanceof Error ? error.message : "格式不正确"}`; } }

/** LocalAgentDownloadManagerProps 下载版本管理组件的属性。 */
type LocalAgentDownloadManagerProps = {
  entries: LocalAgentEntry[];
  editingIndex: number | null;
  editForm: LocalAgentEntry;
  onAdd: () => void;
  onEdit: (index: number) => void;
  onDelete: (index: number) => void;
  onSave: () => void;
  onCancel: () => void;
  onFormChange: (form: LocalAgentEntry) => void;
};

/** LocalAgentDownloadManager 管理本地程序下载版本的添加、编辑和删除。 */
function LocalAgentDownloadManager({ entries, editingIndex, editForm, onAdd, onEdit, onDelete, onSave, onCancel, onFormChange }: LocalAgentDownloadManagerProps) {
  const isEditing = editingIndex !== null;
  return (
    <Box sx={{ mb: 2 }}>
      <Stack direction="row" spacing={1} sx={{ mb: 1.5, justifyContent: "space-between", alignItems: "center" }}>
        <Typography sx={{ fontSize: 15, fontWeight: 700 }}>本地程序下载版本</Typography>
        <Button size="small" startIcon={<AddRoundedIcon />} onClick={onAdd} disabled={isEditing}>添加版本</Button>
      </Stack>
      {entries.length === 0 && !isEditing ? (
        <Typography sx={{ color: "text.secondary", fontSize: 13, py: 1 }}>暂无下载版本，点击上方“添加版本”开始配置。</Typography>
      ) : null}
      {entries.map((entry, index) => (
        <Box key={`${entry.version}-${index}`} sx={{ mb: 1, p: 1.5, border: "1px solid", borderColor: "divider", borderRadius: "6px" }}>
          <Stack direction="row" spacing={1} sx={{ justifyContent: "space-between", alignItems: "center" }}>
            <Box>
              <Typography sx={{ fontWeight: 700, fontSize: 14 }}>{entry.version || "未命名版本"}</Typography>
              <Typography sx={{ color: "text.secondary", fontSize: 12 }}>{entry.note || "无更新说明"}</Typography>
            </Box>
            <Stack direction="row" spacing={0.5}>
              <IconButton size="small" onClick={() => onEdit(index)} disabled={isEditing}><EditRoundedIcon fontSize="small" /></IconButton>
              <IconButton size="small" onClick={() => onDelete(index)} disabled={isEditing} color="error"><DeleteOutlineRoundedIcon fontSize="small" /></IconButton>
            </Stack>
          </Stack>
          {entry.url_win ? <Typography sx={{ fontSize: 11, color: "text.secondary", mt: 0.5, fontFamily: "monospace", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Win: {entry.url_win}</Typography> : null}
          {entry.url_mac ? <Typography sx={{ fontSize: 11, color: "text.secondary", fontFamily: "monospace", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Mac: {entry.url_mac}</Typography> : null}
        </Box>
      ))}
      {isEditing ? (
        <Box sx={{ mt: 1.5, p: 2, border: "1px solid", borderColor: "primary.main", borderRadius: "6px", bgcolor: "action.hover" }}>
          <Typography sx={{ mb: 1.5, fontSize: 14, fontWeight: 700 }}>{editingIndex === -1 ? "添加新版本" : `编辑版本：${entries[editingIndex]?.version || ""}`}</Typography>
          <Stack spacing={1.5}>
            <TextField size="small" fullWidth label="版本号 *" value={editForm.version} onChange={(e) => onFormChange({ ...editForm, version: e.target.value })} placeholder="如 5.3.5" />
            <TextField size="small" fullWidth label="Windows 下载地址" value={editForm.url_win} onChange={(e) => onFormChange({ ...editForm, url_win: e.target.value })} placeholder="https://oss.58it.cn/HRPlusSetup-5.3.5.exe" />
            <TextField size="small" fullWidth label="macOS 下载地址" value={editForm.url_mac} onChange={(e) => onFormChange({ ...editForm, url_mac: e.target.value })} placeholder="https://oss.58it.cn/HRPlusSetup-5.3.5.dmg" />
            <TextField size="small" fullWidth label="SHA256 校验值" value={editForm.sha256} onChange={(e) => onFormChange({ ...editForm, sha256: e.target.value })} placeholder="可选，用于校验安装包完整性" />
            <TextField size="small" fullWidth label="更新说明" value={editForm.note} onChange={(e) => onFormChange({ ...editForm, note: e.target.value })} placeholder="如：修复自动回复评分问题" multiline maxRows={3} />
            <Stack direction="row" spacing={1} sx={{ justifyContent: "flex-end" }}>
              <Button size="small" variant="outlined" onClick={onCancel}>取消</Button>
              <Button size="small" variant="contained" onClick={onSave}>保存</Button>
            </Stack>
          </Stack>
        </Box>
      ) : null}
    </Box>
  );
}
