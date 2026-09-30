/** 本文件负责岗位模板完整的新增、编辑、会员校验、模式联动和提示词管理。 */
"use client";

import AddRoundedIcon from "@mui/icons-material/AddRounded";
import AutoFixHighRoundedIcon from "@mui/icons-material/AutoFixHighRounded";
import DeleteOutlineRoundedIcon from "@mui/icons-material/DeleteOutlineRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import ExpandMoreRoundedIcon from "@mui/icons-material/ExpandMoreRounded";
import LaunchRoundedIcon from "@mui/icons-material/LaunchRounded";
import PlayArrowRoundedIcon from "@mui/icons-material/PlayArrowRounded";
import PlayCircleRoundedIcon from "@mui/icons-material/PlayCircleRounded";
import RestartAltRoundedIcon from "@mui/icons-material/RestartAltRounded";
import StopRoundedIcon from "@mui/icons-material/StopRounded";
import WarningRoundedIcon from "@mui/icons-material/WarningRounded";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Chip,
  Collapse,
  CircularProgress,
  Divider,
  FormControlLabel,
  IconButton,
  Stack,
  Switch,
  TextField,
  Typography,
} from "@mui/material";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import AdminDialog from "@/components/admin/AdminDialog";
import ChoiceCards from "@/components/admin/ChoiceCards";
import ClickableImagePreview from "@/components/admin/ClickableImagePreview";
import {
  EmptyState,
  PageHeader,
  RefreshButton,
  SectionPanel,
} from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";
import PlatformLogo, {
  platformIconSrc,
  platformLabel,
} from "@/components/admin/PlatformLogo";
import PositionFloatingStatus, {
  openPositionFloatingWindow,
  type PositionAnalysisStatus,
  type PositionFloatingStatusValue,
} from "@/components/admin/PositionFloatingStatus";
import { cloudRequest, formatDate, getToken, localRequest } from "@/lib/admin-api";
import { isPlatformOpen, type PlatformConfigLike } from "@/lib/platform-open";
import { reportUserFlow } from "@/lib/user-flow";
import { canUseAI, canUseAutoReply, normalizeSubscription } from "@/lib/subscription";
import {
  agentSupportsAutoReply,
  agentSupportsReGreet,
  autoReplyEnabledForPlatform,
  mergeReplyConfig,
  normalizeFAQList,
  normalizeReplyStats,
  replyStatsText,
  type ReplyStats,
  type FAQEntry,
} from "@/lib/auto-reply";
import { confirmPlatformLoggedInForPosition, openPlatformPositionBrowser, pickPlatformAuthConfig } from "@/lib/platform-login";
import { evaluatePositionStartGuard, latestLocalAgentRelease, positionUsesAI } from "@/lib/position-start-guard";

const HLIEPIN_SHORTCUT_GUIDE_IMAGE_SRC =
  "/assets/help/hliepin-shortcut-search-guide.png";
const PLATFORM_OPEN_ORDER = ["boss", "zhaopin", "hliepin", "liepin"];
const LOG_REFRESH_MS = 3000;
const LOG_LIMIT = 100;
const ALL_LOG_LIMIT = 1000;

/** normalizeFloatingTaskStatus 把本地任务状态转换为置顶小窗支持的明确状态。 */
function normalizeFloatingTaskStatus(value: unknown): PositionFloatingStatusValue {
  const status = String(value || "").trim().toLowerCase();
  if (
    status === "running" ||
    status === "completed" ||
    status === "stopped" ||
    status === "failed"
  ) {
    return status;
  }
  return "stopped";
}

type PositionForm = ReturnType<typeof createEmptyForm>;

type PositionTaskStats = {
  scanned_count: number;
  greeted_count: number;
  skipped_count: number;
};

type FloatingPositionTask = {
  id: string;
  name: string;
  status: PositionFloatingStatusValue;
  followTask: boolean;
  currentStep: string;
  analysis: PositionAnalysisStatus | null;
  scannedCount: number;
  greetedCount: number;
  skippedCount: number;
};

/** PositionsPage 管理岗位筛选、详情识别和 AI 提示词配置。 */
export default function PositionsPage() {
  const router = useRouter();
  const { user, subscription, notify, confirm, agentBase, onboardingConfig } = useAdmin();
  const [items, setItems] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [optimizing, setOptimizing] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [busyPositionID, setBusyPositionID] = useState("");
  const [expandedLogPositionID, setExpandedLogPositionID] = useState("");
  const [logs, setLogs] = useState<Record<string, any[]>>({});
  const [latestTaskStats, setLatestTaskStats] = useState<
    Record<string, PositionTaskStats>
  >({});
  const [logLoadingPositionID, setLogLoadingPositionID] = useState("");
  const [allLogs, setAllLogs] = useState<any[]>([]);
  const [allLogPosition, setAllLogPosition] = useState<any | null>(null);
  const [allLogLoading, setAllLogLoading] = useState(false);
  const [startPositionItem, setStartPositionItem] = useState<any | null>(null);
  const [startTaskType, setStartTaskType] = useState<string[]>(["greeting"]);
  const [replyStats, setReplyStats] = useState<Record<string, ReplyStats>>({});
  const [startLoading, setStartLoading] = useState(false);
  const [startOpeningPlatform, setStartOpeningPlatform] = useState(false);
  const [startStatus, setStartStatus] = useState("");
  const [startError, setStartError] = useState("");
  const [startRequiresUpdate, setStartRequiresUpdate] = useState(false);
  const [floatingStatusWindow, setFloatingStatusWindow] =
    useState<Window | null>(null);
  const [floatingPositionTask, setFloatingPositionTask] =
    useState<FloatingPositionTask | null>(null);
  const [taskFailure, setTaskFailure] = useState<{
    taskID: string;
    positionName: string;
    message: string;
  } | null>(null);
  const shownTaskFailureIDs = useRef<Set<string>>(new Set());
  const [form, setForm] = useState<PositionForm>(createEmptyForm());
  const [platformConfigs, setPlatformConfigs] = useState<PlatformConfigLike[]>(
    [],
  );
  const [defaults, setDefaults] = useState({
    filter_prompt: "",
    open_detail_prompt: "",
    review_prompt: "",
  });
  const aiMembership = canUseAI(subscription);
  const startAutoReplyPlatformOK = autoReplyEnabledForPlatform(
    startPositionItem?.platform_id,
  );
  const startAutoReplyMembershipOK = canUseAutoReply(subscription);
  const startAutoReplyOptionEnabled =
    startAutoReplyPlatformOK && startAutoReplyMembershipOK;
  const startAutoReplyDescription = !startAutoReplyPlatformOK
    ? "该平台尚未开放 AI 自动回复。"
    : !startAutoReplyMembershipOK
      ? "当前会员不支持 AI 自动回复，请升级会员后使用。"
      : "自动回复当前岗位的未读消息，不额外占用打招呼数量。";
  // 复打招呼入口：与自动回复共用平台限制（首批仅 Boss），会员权限复用 canUseAutoReply。
  const startReGreetPlatformOK = autoReplyEnabledForPlatform(
    startPositionItem?.platform_id,
  );
  const startReGreetMembershipOK = canUseAutoReply(subscription);
  const startReGreetOptionEnabled =
    startReGreetPlatformOK && startReGreetMembershipOK;
  const startReGreetDescription = !startReGreetPlatformOK
    ? "该平台尚未开放复打招呼。"
    : !startReGreetMembershipOK
      ? "当前会员不支持复打招呼，请升级会员后使用。"
      : "对之前打过招呼但未回复的候选人再发一次招呼，全局配置在个人配置里。";

  /** load 读取岗位模板和系统默认提示词。 */
  async function load() {
    setLoading(true);
    try {
      const [positions, prompts, platformData] = await Promise.all([
        cloudRequest("/api/positions"),
        cloudRequest("/api/system/default-prompts"),
        cloudRequest("/api/platforms/config/", { auth: false }),
      ]);
      setItems(sortTeamPositions(positions.positions || [], user?.email));
      setDefaults(normalizePrompts(prompts.prompts || prompts || {}));
      setPlatformConfigs(platformData.platforms || platformData.configs || []);
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "岗位模板读取失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  useEffect(() => {
    if (!agentBase || items.length === 0) return;
    void loadLatestTaskStats(items);
  }, [agentBase, items.map((item) => item.id).join(","), user?.email]);

  /** correctStaleRunningStatus 检查云端标记为 running 但本地实际未运行的岗位，纠正为 stopped。 */
  useEffect(() => {
    if (!agentBase || items.length === 0) return;
    const runningItems = items.filter(
      (item) => item.status === "running" && isCurrentUserPosition(item, user?.email),
    );
    if (runningItems.length === 0) return;
    let cancelled = false;
    (async () => {
      await Promise.all(
        runningItems.map(async (item) => {
          try {
            const task = await localRequest(
              agentBase,
              `/api/v1/local/positions/${encodeURIComponent(item.id)}/status`,
            );
            if (cancelled) return;
            const localStatus = normalizeFloatingTaskStatus(task?.status);
            if (localStatus !== "running") {
              await cloudRequest(`/api/positions/${encodeURIComponent(item.id)}/stop`, {
                method: "POST",
              });
              setItems((current) =>
                current.map((p) => (p.id === item.id ? { ...p, status: "stopped" } : p)),
              );
            }
          } catch {
            // 本地请求失败时保留云端状态，不阻塞页面加载。
          }
        }),
      );
    })();
    return () => {
      cancelled = true;
    };
  }, [agentBase, items.map((item) => `${item.id}:${item.status}`).join(","), user?.email]);

  useEffect(() => {
    const expandedPosition = items.find((item) => item.id === expandedLogPositionID);
    if (!expandedPosition || expandedPosition.status !== "running") return undefined;
    const timer = window.setInterval(() => {
      void loadPositionLogs(expandedPosition, { silent: true });
    }, LOG_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [agentBase, expandedLogPositionID, items]);

  useEffect(() => {
    if (!agentBase || !floatingPositionTask?.followTask) return undefined;
    const positionID = floatingPositionTask.id;
    let disposed = false;

    /** refreshFloatingPositionStatus 读取本地任务状态并同步到置顶小窗。 */
    async function refreshFloatingPositionStatus() {
      try {
        const task = await localRequest(
          agentBase,
          `/api/v1/local/positions/${encodeURIComponent(positionID)}/status`,
        );
        if (disposed) return;
        const status = normalizeFloatingTaskStatus(task?.status);
        setFloatingPositionTask((current) =>
          current && current.id === positionID
            ? {
                ...current,
                status,
                followTask: status === "running",
                currentStep: String(task?.current_step || "").trim(),
                analysis: nextFloatingAnalysis(
                  current.analysis,
                  normalizeFloatingAnalysis(task?.analysis),
                ),
                scannedCount: Math.max(0, Number(task?.scanned_count) || 0),
                greetedCount: Math.max(0, Number(task?.greeted_count) || 0),
                skippedCount: Math.max(0, Number(task?.skipped_count) || 0),
              }
            : current,
        );
      } catch {
        // 短暂连接失败时保留上一次状态，避免把仍在运行的任务误报为已停止。
      }
    }

    void refreshFloatingPositionStatus();
    const timer = window.setInterval(
      () => void refreshFloatingPositionStatus(),
      LOG_REFRESH_MS,
    );
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [agentBase, floatingPositionTask?.followTask, floatingPositionTask?.id]);

  /** openCreate 使用免费版可用配置打开新增弹框。 */
  function openCreate() {
    const openPlatformID = firstOpenPlatformID(platformConfigs);
    if (!openPlatformID) {
      notify("暂时没有可用招聘平台，请联系作者", "warning");
      return;
    }
    const next = createEmptyForm();
    next.platform_id = openPlatformID;
    next.mode_default = defaultCreateMode(aiMembership);
    next.detail_mode = defaultCreateDetailMode(
      next.platform_id,
      aiMembership,
    );
    setForm(fillPrompts(next, defaults));
    setAdvancedOpen(false);
    setDialogOpen(true);
  }

  /** openEdit 将岗位完整字段写入弹框并校验会员功能。 */
  async function openEdit(item: any) {
    const next = formFromItem(item, defaults);
    if (
      !aiMembership &&
      (next.mode_default === "ai" || next.detail_mode === "ai")
    ) {
      const go = await confirm(
        "会员功能",
        "该岗位使用了 AI 筛选或 AI 详情识别。当前会员已到期，是否前往订阅页面？",
      );
      if (go) router.push("/admin/subscription");
    }
    setForm(next);
    setAdvancedOpen(false);
    setDialogOpen(true);
  }

  /** save 保存岗位模板并保留旧后端所需字段结构。 */
  async function save() {
    if (!form.name.trim()) return notify("请填写岗位名称", "warning");
    if (!isPlatformOpen(platformConfigs, form.platform_id)) {
      return notify("该平台暂未开放，请联系作者", "warning");
    }
    const detailMode = form.id
      ? normalizeDetailMode(form.platform_id, form.detail_mode)
      : defaultCreateDetailMode(form.platform_id, aiMembership);
    if (
      !aiMembership &&
      (form.mode_default === "ai" || detailMode === "ai")
    )
      return requireMembership();
    setLoading(true);
    try {
      await cloudRequest("/api/positions", {
        method: "POST",
        body: {
          id: form.id,
          platform_id: form.platform_id,
          name: form.name.trim(),
          label: form.label.trim(),
          keywords: splitKeywords(form.keywords),
          exclude_keywords: splitKeywords(form.exclude_keywords),
          description: form.description.trim(),
          greet_message: form.greet_message.trim(),
          is_and_mode: form.is_and_mode,
          common_config: {
            mode_default: form.mode_default,
            detail_mode: detailMode,
            output_structured_resume: form.output_structured_resume,
            hliepin_shortcut_search_name:
              form.hliepin_shortcut_search_name.trim(),
            ...(form.platform_id === "hliepin"
              ? {
                  hliepin_hide_viewed: form.hliepin_hide_viewed,
                  hliepin_hide_contacted: form.hliepin_hide_contacted,
                  hliepin_hide_contact_obtained:
                    form.hliepin_hide_contact_obtained,
                }
              : {}),
            request_phone: form.request_phone,
            request_wechat: form.request_wechat,
            request_resume: form.request_resume,
          },
          ai_config: mergeReplyConfig(
            {
              // 编辑时保留云端已有的其他 ai_config 键，表单键覆盖同名值。
              ...(form.id
                ? items.find((item) => item.id === form.id)?.ai_config || {}
                : {}),
              position_requirement: form.position_requirement,
              filter_prompt: form.filter_prompt || defaults.filter_prompt,
              greet_prompt: form.filter_prompt || defaults.filter_prompt,
              click_prompt: form.filter_prompt || defaults.filter_prompt,
              open_detail_prompt:
                form.open_detail_prompt || defaults.open_detail_prompt,
              review_prompt: normalizePrompt(form.review_prompt),
              detail_score_threshold: Number(form.detail_score_threshold || 60),
              greet_score_threshold: Number(form.greet_score_threshold || 70),
              request_score_threshold: Number(
                form.request_score_threshold ??
                  form.greet_score_threshold ??
                  70,
              ),
              re_greet_prompt: normalizePrompt(form.re_greet_prompt),
              re_greet_skip_refused: Boolean(form.re_greet_skip_refused),
            },
            form.reply_prompt,
            form.reply_faq,
            form.reply_reject_template,
          ),
          keyword_config: {},
          match_limit: form.match_limit > 0 ? Number(form.match_limit) : 50,
          enable_sound: form.enable_sound,
          enable_thinking: form.enable_thinking,
        },
      });
      notify(form.id ? "岗位模板已更新" : "岗位模板已创建", "success");
      setDialogOpen(false);
      await load();
    } catch (error) {
      notify(error instanceof Error ? error.message : "保存岗位失败", "error");
    } finally {
      setLoading(false);
    }
  }

  /** remove 删除指定岗位模板。 */
  async function remove(item: any) {
    if (!(await confirm("删除岗位模板", `确认删除“${item.name}”吗？`))) return;
    try {
      await cloudRequest(`/api/positions/${item.id}`, { method: "DELETE" });
      notify("岗位模板已删除", "success");
      await load();
    } catch (error) {
      notify(error instanceof Error ? error.message : "删除失败", "error");
    }
  }

  /** openStartPosition 展开岗位日志并打开启动确认弹框。 */
  function openStartPosition(item: any) {
    if (!agentBase) return notify("请先启动本地程序", "warning");
    setExpandedLogPositionID(item.id);
    void loadPositionLogs(item);
    setStartStatus("");
    setStartError("");
    setStartOpeningPlatform(false);
    setStartRequiresUpdate(false);
    setStartTaskType(["greeting"]);
    setStartPositionItem(item);
  }

  /** closeStartDialog 清理岗位启动弹框中的运行状态和错误信息。 */
  function closeStartDialog() {
    if (startLoading || startOpeningPlatform) return;
    setStartPositionItem(null);
    setStartStatus("");
    setStartError("");
    setStartOpeningPlatform(false);
    setStartRequiresUpdate(false);
    setStartTaskType(["greeting"]);
  }

  /** openStartPlatformForFiltering 打开当前岗位对应的招聘平台页面，供用户先手动设置基础筛选条件。 */
  async function openStartPlatformForFiltering() {
    const item = startPositionItem;
    if (!item || !agentBase || startOpeningPlatform) return;
    if (!isPlatformOpen(platformConfigs, item.platform_id)) {
      setStartStatus("这个招聘平台暂时还没开放，请联系作者确认。");
      return;
    }
    setStartOpeningPlatform(true);
    setStartStatus("正在打开当前岗位对应的招聘平台页面...");
    try {
      const auth = pickPlatformAuthConfig(platformConfigs, item.platform_id);
      await openPlatformPositionBrowser(agentBase, item.platform_id, auth);
      setStartStatus("招聘平台已打开。请在平台页面筛选好基础条件，再回来点击“我已筛选好，立即开始”。");
    } catch (error) {
      const message = error instanceof Error ? error.message : "打开招聘平台失败";
      setStartStatus(message);
      setStartError(message);
    } finally {
      setStartOpeningPlatform(false);
    }
  }

  /** checkPositionStartGuard 检查 AI 余额和本地程序版本是否满足启动要求，通过时返回本地健康数据。 */
  async function checkPositionStartGuard(item: any) {
    const usesAI = positionUsesAI(item);
    setStartStatus(usesAI ? "正在检查 AI 配置和本地程序版本..." : "正在检查本地程序版本...");
    try {
      let runtimeConfig = onboardingConfig;
      if (!latestLocalAgentRelease(runtimeConfig).version) {
        const runtimePayload = await cloudRequest("/api/runtime/config");
        runtimeConfig = runtimePayload.config || runtimePayload || {};
      }
      // 检查用户是否配置了自定义 AI API，有则跳过系统余额检查。
      let skipBalanceCheck = false;
      if (usesAI) {
        try {
          const aiConfigPayload = await cloudRequest("/api/config/effective-ai");
          skipBalanceCheck = Boolean(aiConfigPayload?.config?.api_key_set);
        } catch {
          // 读取 AI 配置失败时保守处理，继续走余额检查。
        }
      }
      const [health, walletPayload] = await Promise.all([
        localRequest(agentBase, "/health"),
        usesAI && !skipBalanceCheck ? cloudRequest("/api/ai-wallet") : Promise.resolve(null),
      ]);
      const release = latestLocalAgentRelease(runtimeConfig);
      const guardFailure = evaluatePositionStartGuard(
        walletPayload?.wallet || walletPayload,
        health.version || health.agent_version,
        release.version,
        usesAI,
        skipBalanceCheck,
      );
      if (guardFailure) {
        await reportUserFlow({ step: "position_started", status: "blocked", reason_code: guardFailure.code, message: guardFailure.message, source: "position_start_guard", position_id: item.id }).catch(() => undefined);
        setStartStatus(guardFailure.message);
        setStartRequiresUpdate(guardFailure.code === "agent_version_outdated");
        setStartError(guardFailure.code === "agent_version_outdated" ? "" : guardFailure.message);
        return null;
      }
      setStartRequiresUpdate(false);
      return health;
    } catch (error) {
      const message = error instanceof Error
        ? `启动条件检查未完成：${error.message}。请刷新后重试。`
        : "启动条件检查未完成，请刷新后重试。";
      setStartStatus(message);
      setStartError(message);
      setStartRequiresUpdate(false);
      await reportUserFlow({ step: "position_started", status: "blocked", reason_code: "position_start_guard_unavailable", message, source: "position_start_guard", position_id: item.id }).catch(() => undefined);
      return null;
    }
  }

  /** confirmStartPosition 在确认弹框中完成启动检查、登录确认和岗位启动。 */
  async function confirmStartPosition() {
    if (startRequiresUpdate) {
      window.location.reload();
      return;
    }
    const item = startPositionItem;
    if (!item || !agentBase) return;
    const floatingWindowPromise = openPositionFloatingWindow();
    let started = false;
    setStartLoading(true);
    setBusyPositionID(item.id);
    setStartStatus("正在检查岗位启动条件...");
    setStartError("");
    try {
      const pipWindow = await floatingWindowPromise;
      if (pipWindow) {
        setFloatingStatusWindow(pipWindow);
        setFloatingPositionTask({
          id: item.id,
          name: String(item.name || "当前岗位"),
          status: "running",
          followTask: false,
          currentStep: "正在检查岗位启动条件",
          analysis: null,
          scannedCount: 0,
          greetedCount: 0,
          skippedCount: 0,
        });
      }
      const health = await checkPositionStartGuard(item);
      if (!health) return;
      const subscriptionData = await cloudRequest("/api/subscription/status");
      const currentSubscription = normalizeSubscription(
        subscriptionData.subscription,
      );
      if (!isPlatformOpen(platformConfigs, item.platform_id)) {
        const message = "该招聘平台暂未开放，请联系管理员。";
        setStartStatus(message);
        setStartError(message);
        return;
      }
      if (startTaskType.includes("auto_reply") && !startTaskType.includes("greeting") && !startTaskType.includes("re_greet")) {
        // 仅自动回复：由本地程序准备消息页，前端只做能力和权限检查。
        if (!agentSupportsAutoReply(health)) {
          const message = "当前本地程序版本还不支持 AI 自动回复，请更新本地程序后重试。";
          setStartStatus(message);
          setStartError(message);
          return;
        }
        if (!canUseAutoReply(currentSubscription)) {
          const message = "AI 自动回复是会员功能，请订阅后重试。";
          setStartStatus(message);
          setStartError(message);
          await reportUserFlow({ step: "position_started", status: "blocked", reason_code: "subscription_expired", message, source: "position_start", position_id: item.id }).catch(() => undefined);
          return;
        }
        setStartStatus("正在启动 AI 自动回复，本地程序会自动打开消息页...");
      } else if (startTaskType.includes("re_greet") && !startTaskType.includes("greeting") && !startTaskType.includes("auto_reply")) {
        // 仅复打招呼：由本地程序准备消息页并从云端拉复打名单。
        if (!agentSupportsReGreet(health)) {
          const message = "当前本地程序版本还不支持复打招呼，请更新本地程序后重试。";
          setStartStatus(message);
          setStartError(message);
          return;
        }
        if (!canUseAutoReply(currentSubscription)) {
          const message = "复打招呼是会员功能，请订阅后重试。";
          setStartStatus(message);
          setStartError(message);
          await reportUserFlow({ step: "position_started", status: "blocked", reason_code: "subscription_expired", message, source: "position_start", position_id: item.id }).catch(() => undefined);
          return;
        }
        setStartStatus("正在启动复打招呼，本地程序会自动打开消息页...");
      } else {
        const auth = pickPlatformAuthConfig(platformConfigs, item.platform_id);
        setStartStatus("正在打开招聘平台，请确认账号已登录。");
        await openPlatformPositionBrowser(agentBase, item.platform_id, auth);
        try {
          await confirmPlatformLoggedInForPosition(agentBase, auth, (message) =>
            setStartStatus(message),
          );
          await reportUserFlow({ step: "platform_login_verified", source: "position_start", position_id: item.id });
        } catch (loginError) {
          const message = loginError instanceof Error
            ? loginError.message
            : "招聘平台还没登录，请先在浏览器里完成登录。";
          setStartStatus(message);
          setStartError(message);
          await reportUserFlow({ step: "platform_login_verified", status: "blocked", reason_code: "platform_not_logged_in", message, source: "position_start", position_id: item.id }).catch(() => undefined);
          return;
        }
      }
      const usesAI = positionUsesAI(item);
      if (usesAI && !canUseAI(currentSubscription)) {
        const message = "该岗位使用了会员 AI 功能，请订阅后重试。";
        setStartStatus(message);
        setStartError(message);
        await reportUserFlow({ step: "position_started", status: "blocked", reason_code: "subscription_expired", message, source: "position_start", position_id: item.id }).catch(() => undefined);
        return;
      }
      if (!currentSubscription.active) notify("当前是免费版，今天的打招呼数量会按免费额度来，我会省着点用。", "info");
      setStartStatus(
        startTaskType.includes("auto_reply") && !startTaskType.includes("greeting") && !startTaskType.includes("re_greet")
          ? "正在启动 AI 自动回复..."
          : startTaskType.includes("re_greet") && !startTaskType.includes("greeting") && !startTaskType.includes("auto_reply")
            ? "正在启动复打招呼..."
            : "登录确认好了，正在启动岗位...",
      );
      // 构造 task_type：多选时传逗号分隔字符串，单选打招呼时不传（默认行为）。
      const taskTypePayload = startTaskType.length === 1 && startTaskType[0] === "greeting"
        ? undefined
        : startTaskType.join(",");
      await localRequest(agentBase, `/api/v1/local/positions/${encodeURIComponent(item.id)}/run`, {
        method: "POST",
        body: {
          token: getToken(),
          enable_greet: true,
          ...(taskTypePayload ? { task_type: taskTypePayload } : {}),
        },
      });
      started = true;
      setFloatingPositionTask((current) =>
        current && current.id === item.id
          ? { ...current, status: "running", followTask: true }
          : current,
      );
      await reportUserFlow({ step: "position_started", source: "position_start", position_id: item.id });
      notify("岗位已经开始跑了，我会老实记日志", "success");
      setStartPositionItem(null);
      setStartStatus("");
      setStartError("");
      await Promise.all([load(), loadPositionLogs(item)]);
    } catch (error) {
      const message = error instanceof Error ? error.message : "岗位启动失败";
      setStartStatus(message);
      setStartError(message);
      await reportUserFlow({ step: "position_started", status: "blocked", reason_code: positionStartReason(message), message, source: "position_start", position_id: item.id }).catch(() => undefined);
    } finally {
      if (!started) {
        setFloatingPositionTask((current) =>
          current && current.id === item.id
            ? { ...current, status: "stopped", followTask: false }
            : current,
        );
      }
      setBusyPositionID("");
      setStartLoading(false);
    }
  }

  /** stopPosition 停止本地岗位运行，但保持浏览器打开。 */
  async function stopPosition(item: any) {
    if (!agentBase) return notify("本地程序还没连上", "warning");
    setExpandedLogPositionID("");
    setBusyPositionID(item.id);
    try {
      await localRequest(agentBase, `/api/v1/local/positions/${encodeURIComponent(item.id)}/stop`, {
        method: "POST",
        body: { token: getToken() },
        timeoutMS: 220000,
      });
      setFloatingPositionTask((current) =>
        current && current.id === item.id
          ? { ...current, status: "stopped", followTask: false }
          : current,
      );
      notify("岗位已停下，浏览器先给你留着", "success");
      await load();
      // load 后再更新一次，确保按钮状态正确（云端可能还没同步）
      setItems((current) => current.map((p) => p.id === item.id ? { ...p, status: "stopped" } : p));
    } catch (error) {
      notify(error instanceof Error ? error.message : "停止岗位失败", "error");
    } finally {
      setBusyPositionID("");
    }
  }

  /** forceStopPosition 强制停止任务，直接取消 context 不等优雅结束。 */
  async function forceStopPosition(item: any) {
    if (!agentBase) return notify("本地程序还没连上", "warning");
    setExpandedLogPositionID("");
    setBusyPositionID(item.id);
    try {
      // 先获取当前任务 ID
      const taskData = await localRequest(agentBase, `/api/v1/local/positions/${encodeURIComponent(item.id)}/status`);
      const taskID = taskData?.task?.task_id || taskData?.task?.id;
      if (!taskID) {
        notify("没找到运行中的任务", "warning");
        return;
      }
      // 调用强制停止 API
      await localRequest(agentBase, `/api/v1/tasks/force-stop`, {
        method: "POST",
        body: { task_id: taskID },
        timeoutMS: 30000,
      });
      setFloatingPositionTask((current) =>
        current && current.id === item.id
          ? { ...current, status: "stopped", followTask: false }
          : current,
      );
      notify("已强制停止，任务已中断", "success");
      await load();
      setItems((current) => current.map((p) => p.id === item.id ? { ...p, status: "stopped" } : p));
    } catch (error) {
      notify(error instanceof Error ? error.message : "强制停止失败", "error");
    } finally {
      setBusyPositionID("");
    }
  }

  /** togglePositionLogs 展开或收起岗位累计日志，不主动清空历史日志。 */
  async function togglePositionLogs(item: any) {
    if (expandedLogPositionID === item.id) {
      setExpandedLogPositionID("");
      return;
    }
    if (!agentBase) return notify("本地程序还没连上", "warning");
    setExpandedLogPositionID(item.id);
    await loadPositionLogs(item);
  }

  /** loadPositionLogs 读取指定岗位最近的本地日志并更新卡片。 */
  async function loadPositionLogs(item: any, options: { silent?: boolean } = {}) {
    if (!agentBase) return;
    if (!options.silent) setLogLoadingPositionID(item.id);
    try {
      const data = await localRequest(agentBase, `/api/v1/local/positions/${encodeURIComponent(item.id)}/logs?limit=${LOG_LIMIT}`);
      setLogs((current) => ({ ...current, [item.id]: data.logs || [] }));
      const task = data.task;
      if (task) {
        setLatestTaskStats((current) => ({
          ...current,
          [item.id]: normalizePositionTaskStats(task),
        }));
        if (task.reply_stats) {
          setReplyStats((current) => ({
            ...current,
            [item.id]: normalizeReplyStats(task.reply_stats),
          }));
        }
      }
      const taskStatus = String(task?.status || "").trim();
      if (taskStatus) {
        setItems((current) => {
          let changed = false;
          const next = current.map((position) => {
            if (position.id !== item.id || position.status === taskStatus) return position;
            changed = true;
            return { ...position, status: taskStatus };
          });
          return changed ? next : current;
        });
        if (item.status === "running" && taskStatus !== "running") {
          void load();
        }
      }
      const taskID = String(task?.task_id || "").trim();
      const errorMessage = String(task?.error_message || "").trim();
      if (
        taskStatus === "failed" &&
        taskID &&
        errorMessage &&
        !shownTaskFailureIDs.current.has(taskID)
      ) {
        shownTaskFailureIDs.current.add(taskID);
        setTaskFailure({
          taskID,
          positionName: String(item.name || "当前岗位"),
          message: errorMessage,
        });
      }
    } catch (error) {
      if (!options.silent) notify(error instanceof Error ? error.message : "日志读取失败", "error");
    } finally {
      if (!options.silent) setLogLoadingPositionID("");
    }
  }

  /** loadLatestTaskStats 读取各岗位最近一次本地任务统计。 */
  async function loadLatestTaskStats(positionItems: any[]) {
    if (!agentBase) return;
    const next: Record<string, PositionTaskStats> = {};
    const nextReplyStats: Record<string, ReplyStats> = {};
    await Promise.all(
      positionItems.filter((item) => isCurrentUserPosition(item, user?.email)).map(async (item) => {
        try {
          const task = await localRequest(
            agentBase,
            `/api/v1/local/positions/${encodeURIComponent(item.id)}/status`,
          );
          next[item.id] = normalizePositionTaskStats(task);
          if (task?.reply_stats) {
            nextReplyStats[item.id] = normalizeReplyStats(task.reply_stats);
          }
        } catch {
          // 没有本地任务记录时保留零值，不能影响岗位列表加载。
        }
      }),
    );
    setLatestTaskStats(next);
    setReplyStats(nextReplyStats);
  }

  /** clearPositionLogs 二次确认后清空指定岗位保存在本地程序中的日志。 */
  async function clearPositionLogs(item: any) {
    if (!agentBase) return notify("本地程序还没连上", "warning");
    const approved = await confirm(
      "清空岗位日志",
      "确认清空该岗位的全部本地日志吗？清空后无法恢复。",
    );
    if (!approved) return;
    try {
      await localRequest(agentBase, `/api/v1/local/positions/${encodeURIComponent(item.id)}/logs`, {
        method: "DELETE",
      });
      setLogs((current) => ({ ...current, [item.id]: [] }));
      if (allLogPosition?.id === item.id) setAllLogs([]);
      notify("岗位日志已清空。", "success");
    } catch (error) {
      notify(error instanceof Error ? error.message : "日志清空失败，请重试。", "error");
    }
  }

  /** loadAllPositionLogs 读取指定岗位保留的全部本地日志并打开弹框。 */
  async function loadAllPositionLogs(item: any) {
    if (!agentBase) return notify("本地程序还没连上", "warning");
    setAllLogPosition(item);
    setAllLogLoading(true);
    try {
      const data = await localRequest(agentBase, `/api/v1/local/positions/${encodeURIComponent(item.id)}/logs?limit=${ALL_LOG_LIMIT}`);
      setAllLogs(data.logs || []);
    } catch (error) {
      notify(error instanceof Error ? error.message : "全部日志读取失败", "error");
    } finally {
      setAllLogLoading(false);
    }
  }

  /** copyAllPositionLogs 复制当前弹框中的完整岗位日志。 */
  async function copyAllPositionLogs() {
    try {
      await navigator.clipboard.writeText(buildPositionLogText(allLogs));
      notify("全部日志已复制", "success");
    } catch {
      notify("复制失败，请手动选择日志内容", "warning");
    }
  }

  /** optimizeRequirement 调用用户 AI 配置整理岗位要求。 */
  async function optimizeRequirement() {
    if (!form.position_requirement.trim())
      return notify("请先填写岗位要求", "warning");
    setOptimizing(true);
    try {
      const data = await cloudRequest("/api/positions/optimize-requirement", {
        method: "POST",
        body: {
          text: form.position_requirement,
        },
      });
      setForm((current) => ({
        ...current,
        position_requirement:
          data.optimized || data.text || current.position_requirement,
      }));
      notify("岗位要求已优化", "success");
    } catch (error) {
      const message = error instanceof Error ? error.message : "AI 优化失败";
      // 检测 AI 配置相关错误，提示用户前往 AI 配置页
      if (
        message.includes("AI 配置") ||
        message.includes("个人配置") ||
        message.includes("AI 配置不完整") ||
        message.includes("AI 配置未启用")
      ) {
        const go = await confirm(
          "AI 配置缺失",
          "使用 AI 功能需要先在 AI 配置里填写 API 地址、模型和 Key，是否前往设置？",
        );
        if (go) router.push("/admin/ai-config");
        return;
      }
      notify(message, "error");
    } finally {
      setOptimizing(false);
    }
  }

  /** selectMode 选择筛选模式并执行会员提醒。 */
  async function selectMode(value: string) {
    if (value === "ai" && !aiMembership) return requireMembership();
    setForm((current) => ({ ...current, mode_default: value }));
  }

  /** selectDetailMode 选择详情模式并执行平台与会员联动。 */
  async function selectDetailMode(value: string) {
    if (form.platform_id === "boss" && value === "dom")
      return notify("Boss直聘不支持 DOM 详情识别", "warning");
    if (isDOMOnlyPlatform(form.platform_id) && value !== "dom")
      return notify(
        `${platformLabel(form.platform_id)}只能用 DOM 详情识别`,
        "warning",
      );
    if (value === "ai" && !aiMembership) return requireMembership();
    setForm((current) => ({ ...current, detail_mode: value }));
  }

  /** selectPlatform 切换平台并修正平台不支持的详情模式。 */
  function selectPlatform(value: string) {
    if (!isPlatformOpen(platformConfigs, value)) {
      notify("该平台暂未开放，请联系作者", "warning");
      return;
    }
    setForm((current) => ({
      ...current,
      platform_id: value,
      detail_mode: current.id
        ? normalizeDetailMode(value, current.detail_mode)
        : defaultCreateDetailMode(value, aiMembership),
    }));
  }

  /** requireMembership 引导免费用户前往订阅页面。 */
  async function requireMembership() {
    const go = await confirm(
      "该功能需要订阅会员",
      "AI 筛选和 AI 详情识别属于会员功能，是否前往订阅页面？",
    );
    if (go) router.push("/admin/subscription");
  }

  return (
    <>
      <PositionFloatingStatus
        pipWindow={floatingStatusWindow}
        status={floatingPositionTask?.status || "stopped"}
        currentStep={floatingPositionTask?.currentStep || ""}
        analysis={floatingPositionTask?.analysis || null}
        scannedCount={floatingPositionTask?.scannedCount || 0}
        greetedCount={floatingPositionTask?.greetedCount || 0}
        skippedCount={floatingPositionTask?.skippedCount || 0}
        onClosed={() => {
          setFloatingStatusWindow(null);
          setFloatingPositionTask(null);
        }}
      />
      <PageHeader
        title='岗位管理'
        description='岗位模板决定首次筛选、详情识别和最终打招呼判断。'
        actions={
          <>
            <Button
              component={Link}
              href='/admin/position-runs'
              startIcon={<PlayCircleRoundedIcon />}
              disabled={loading}
            >
              任务记录
            </Button>
            <Button
              variant='contained'
              startIcon={<AddRoundedIcon />}
              disabled={loading}
              onClick={openCreate}
            >
              新建岗位
            </Button>
            <RefreshButton loading={loading} onClick={() => void load()} />
          </>
        }
      />
      {items.length ? (
        <Stack spacing={1.5}>
          {items.map((item) => (
            <Box
              key={item.id}
              sx={{
                p: { xs: 1.5, sm: 2 },
                border: "1px solid",
                borderColor: "divider",
                borderRadius: "8px",
                bgcolor: "background.paper",
              }}
            >
              <Stack
                direction='row'
                spacing={2}
                sx={{ alignItems: "flex-start" }}
              >
                <PlatformLogo platformID={item.platform_id} size={42} />
                <Stack
                  direction={{ xs: "column", md: "row" }}
                  spacing={1.5}
                  sx={{
                    flex: 1,
                    minWidth: 0,
                    alignItems: { md: "center" },
                    justifyContent: "space-between",
                  }}
                >
                  <Box sx={{ minWidth: 0 }}>
                    <Stack
                      direction='row'
                      spacing={0.75}
                      sx={{ alignItems: "center", minWidth: 0 }}
                    >
                      <Typography noWrap sx={{ fontWeight: 760, minWidth: 0 }}>
                        {item.name}
                      </Typography>
                      {item.label ? (
                        <Chip
                          size='small'
                          variant='outlined'
                          label={item.label}
                          title={item.label}
                          sx={{
                            height: 20,
                            fontSize: 12,
                            flexShrink: 0,
                            color: "text.secondary",
                            borderColor: "divider",
                            "& .MuiChip-label": { px: 0.75 },
                          }}
                        />
                      ) : null}
                    </Stack>
                    <Typography
                      sx={{ mt: 0.4, color: "text.secondary", fontSize: 12, overflowWrap: "anywhere" }}
                    >
                      创建人：{item.creator_email || "当前账号"} · 创建于：
                      {formatDate(item.created_at) || "--"}
                    </Typography>
                    <Typography
                      sx={{
                        mt: 0.5,
                        color: "text.secondary",
                        fontSize: 13,
                        overflowWrap: "anywhere",
                      }}
                    >
                      {platformLabel(item.platform_id)} ·{" "}
                      {item.common_config?.mode_default === "ai"
                        ? "AI 筛选"
                        : "关键词筛选"}{" "}
                      · 详情：
                      {detailModeLabel(item.common_config?.detail_mode)} ·
                      关键词：{(item.keywords || []).join(" / ") || "无"}
                    </Typography>
                  </Box>
                  {isCurrentUserPosition(item, user?.email) ? (
                  <Stack direction='row' spacing={1} sx={{ flexWrap: "wrap" }}>
                    {item.status === "running" ? (
                      <>
                        <Button
                          color='error'
                          variant='contained'
                          startIcon={<StopRoundedIcon />}
                          disabled={busyPositionID === item.id}
                          onClick={() => void stopPosition(item)}
                        >
                          停止
                        </Button>
                        <Button
                          color='error'
                          variant='outlined'
                          startIcon={<WarningRoundedIcon />}
                          disabled={busyPositionID === item.id}
                          onClick={() => void forceStopPosition(item)}
                          title="强制停止：直接中断任务，不等当前候选人处理完"
                        >
                          强制停止
                        </Button>
                      </>
                    ) : (
                      <Button
                        color='primary'
                        variant='contained'
                        startIcon={<PlayArrowRoundedIcon />}
                        disabled={busyPositionID === item.id}
                        onClick={() => openStartPosition(item)}
                      >
                        开始
                      </Button>
                    )}
                    <Button onClick={() => void togglePositionLogs(item)}>
                      {expandedLogPositionID === item.id ? "收起日志" : "日志"}
                    </Button>
                    <Button
                      startIcon={<EditRoundedIcon />}
                      onClick={() => void openEdit(item)}
                    >
                      编辑
                    </Button>
                    <Button
                      color='error'
                      startIcon={<DeleteOutlineRoundedIcon />}
                      onClick={() => void remove(item)}
                    >
                      删除
                    </Button>
                  </Stack>
                  ) : (
                    <Typography sx={{ color: "text.secondary", fontSize: 13, whiteSpace: "nowrap" }}>
                      团队岗位，仅查看
                    </Typography>
                  )}
                </Stack>
              </Stack>
              <Typography sx={{ mt: 1, color: "text.secondary", fontSize: 13 }}>
                今日 {item.today_greeted_count || 0}
                {isCurrentUserPosition(item, user?.email) ? < >
                  {" "}· 本次（扫描 {latestTaskStats[item.id]?.scanned_count || 0} · 打招呼{" "}
                  {latestTaskStats[item.id]?.greeted_count || 0} · 跳过{" "}
                  {latestTaskStats[item.id]?.skipped_count || 0}）
                </> : null}
              </Typography>
              {replyStats[item.id] ? (
                <Typography sx={{ mt: 0.5, color: "text.secondary", fontSize: 13 }}>
                  AI 回复（{replyStatsText(replyStats[item.id])}）
                </Typography>
              ) : null}
              <Collapse in={isCurrentUserPosition(item, user?.email) && expandedLogPositionID === item.id}>
                <PositionLogPanel
                  logs={logs[item.id] || []}
                  loading={logLoadingPositionID === item.id}
                  onRefresh={() => void loadPositionLogs(item)}
                  onViewAll={() => void loadAllPositionLogs(item)}
                  onClear={() => void clearPositionLogs(item)}
                />
              </Collapse>
            </Box>
          ))}
        </Stack>
      ) : (
        <SectionPanel>
          <EmptyState text='暂无岗位模板' />
        </SectionPanel>
      )}
      <AdminDialog
        open={Boolean(startPositionItem)}
        title={startError ? "岗位还没启动成功" : "开始招聘岗位"}
        confirmText={startError ? "我知道了" : startRequiresUpdate ? "立即更新" : startTaskType.length > 1 ? "开始运行" : startTaskType.includes("auto_reply") && !startTaskType.includes("greeting") ? "开始 AI 自动回复" : startTaskType.includes("re_greet") && !startTaskType.includes("greeting") ? "开始复打招呼" : "我已筛选好，立即开始"}
        showCancel={!startError}
        loading={startLoading}
        loadingText='启动中'
        onClose={closeStartDialog}
        onConfirm={() => startError ? closeStartDialog() : void confirmStartPosition()}
      >
        {startError ? (
          <Alert severity='error' variant='outlined'>
            <Typography sx={{ fontWeight: 700, lineHeight: 1.7 }}>
              {startError}
            </Typography>
            <Typography sx={{ mt: 0.75, color: "text.secondary", lineHeight: 1.7 }}>
              岗位未启动。请按上方提示处理后重新点击开始。
            </Typography>
          </Alert>
        ) : (
          <Stack spacing={1.5}>
          <Typography>
            确认开始“{startPositionItem?.name || ""}”吗？
          </Typography>
          <Box>
            <Typography sx={{ mb: 0.5, fontSize: 14, fontWeight: 600 }}>任务类型（可多选）</Typography>
            <Stack direction='row' spacing={2} sx={{ flexWrap: "wrap" }}>
              <FormControlLabel
                control={
                  <Checkbox
                    size='small'
                    checked={startTaskType.includes("greeting")}
                    onChange={(e) =>
                      setStartTaskType((prev) =>
                        e.target.checked
                          ? [...prev, "greeting"]
                          : prev.filter((t) => t !== "greeting"),
                      )
                    }
                  />
                }
                label='打招呼'
              />
              <FormControlLabel
                control={
                  <Checkbox
                    size='small'
                    checked={startTaskType.includes("auto_reply")}
                    disabled={!startAutoReplyOptionEnabled}
                    onChange={(e) =>
                      setStartTaskType((prev) =>
                        e.target.checked
                          ? [...prev, "auto_reply"]
                          : prev.filter((t) => t !== "auto_reply"),
                      )
                    }
                  />
                }
                label={
                  <Stack direction='row' spacing={0.75} sx={{ alignItems: 'center' }}>
                    <span>AI 自动回复</span>
                    <Chip
                      size='small'
                      label='PRO 用户专享功能'
                      sx={{
                        height: 20,
                        fontSize: 11,
                        fontWeight: 600,
                        color: '#fff',
                        backgroundColor: 'primary.main',
                        '& .MuiChip-label': { px: 0.75 },
                      }}
                    />
                  </Stack>
                }
              />
              <FormControlLabel
                control={
                  <Checkbox
                    size='small'
                    checked={startTaskType.includes("re_greet")}
                    disabled={!startReGreetOptionEnabled}
                    onChange={(e) =>
                      setStartTaskType((prev) =>
                        e.target.checked
                          ? [...prev, "re_greet"]
                          : prev.filter((t) => t !== "re_greet"),
                      )
                    }
                  />
                }
                label={
                  <Stack direction='row' spacing={0.75} sx={{ alignItems: 'center' }}>
                    <span>复打招呼</span>
                    <Chip
                      size='small'
                      label='PRO 用户专享功能'
                      sx={{
                        height: 20,
                        fontSize: 11,
                        fontWeight: 600,
                        color: '#fff',
                        backgroundColor: 'primary.main',
                        '& .MuiChip-label': { px: 0.75 },
                      }}
                    />
                  </Stack>
                }
              />
            </Stack>
            {!startAutoReplyOptionEnabled && !startReGreetOptionEnabled ? (
              <Typography sx={{ fontSize: 12, color: "text.secondary", mt: 0.25 }}>
                {!startAutoReplyOptionEnabled ? startAutoReplyDescription : startReGreetDescription}
              </Typography>
            ) : null}
          </Box>
          {startTaskType.includes("greeting") ? (
            <>
              <Alert severity='warning' variant='outlined'>
                <Typography sx={{ fontWeight: 700, lineHeight: 1.7 }}>
                  强烈建议您先点击下方“打开平台，并筛选条件”，在招聘平台中设置好年龄、学历、地区等基础筛选条件，再回来开始任务。基础筛选会直接影响候选人结果，此步骤非常重要。
                </Typography>
              </Alert>
              <Button
                fullWidth
                variant='outlined'
                size='large'
                disabled={startLoading || startOpeningPlatform}
                startIcon={startOpeningPlatform ? <CircularProgress color='inherit' size={18} /> : <LaunchRoundedIcon />}
                onClick={() => void openStartPlatformForFiltering()}
                sx={{ py: 1.15, fontWeight: 760 }}
              >
                {startOpeningPlatform ? "正在打开招聘平台" : "打开平台，并筛选条件"}
              </Button>
            </>
          ) : (
            <Typography sx={{ color: "text.secondary", fontSize: 13, lineHeight: 1.7 }}>
              本地程序会自动打开招聘平台消息页，只回复属于当前岗位的未读文字消息；如果招聘平台还没登录或岗位对不上，任务会停止并提示。已由 AI 回复过的消息不会重复回复。
            </Typography>
          )}
          <Box sx={{ minHeight: 24 }}>
            {startStatus ? (
              <Typography color={isPositionStartErrorStatus(startStatus) ? "error" : "text.secondary"}>
                {startStatus}
              </Typography>
            ) : null}
          </Box>
          </Stack>
        )}
      </AdminDialog>
      <AdminDialog
        open={Boolean(taskFailure)}
        title='岗位已经停下来了'
        confirmText='我知道了'
        showCancel={false}
        onClose={() => setTaskFailure(null)}
        onConfirm={() => setTaskFailure(null)}
      >
        <Alert severity='error' variant='outlined'>
          <Typography sx={{ fontWeight: 700, lineHeight: 1.7 }}>
            “{taskFailure?.positionName || "当前岗位"}”运行时遇到了问题，已经安全停止。
          </Typography>
          <Typography sx={{ mt: 0.75, lineHeight: 1.7 }}>
            {taskFailure?.message || "错误原因已经记到岗位日志里。"}
          </Typography>
        </Alert>
      </AdminDialog>
      <AdminDialog
        open={Boolean(allLogPosition)}
        title='查看全部岗位日志'
        description={`读取当前岗位已保留的全部日志，单次最多 ${ALL_LOG_LIMIT} 条，可复制后发给作者排查。`}
        confirmText='复制全部'
        cancelText='关闭'
        loading={allLogLoading}
        maxWidth='lg'
        onClose={() => {
          setAllLogPosition(null);
          setAllLogs([]);
        }}
        onConfirm={() => void copyAllPositionLogs()}
      >
        <PositionLogList logs={allLogs} maxHeight='60vh' />
      </AdminDialog>
      <AdminDialog
        open={dialogOpen}
        title={form.id ? "编辑岗位模板" : "新建岗位模板"}
        description='按工作流顺序填写，从上到下依次配置。'
        maxWidth='md'
        confirmText={form.id ? "保存修改" : "创建岗位"}
        loading={loading}
        confirmDisabled={!form.name.trim()}
        onClose={() => setDialogOpen(false)}
        onConfirm={() => void save()}
      >
        <Stack spacing={3}>
          <Box>
            <Typography
              component='h3'
              sx={{ mb: 1.5, fontSize: 17, fontWeight: 780 }}
            >
              基础信息
            </Typography>
            <TextField
              label='岗位名称'
              value={form.name}
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
              fullWidth
              placeholder='例如：服装带货主播'
              helperText='岗位名称必须和平台岗位岗位名称保持一致。(请前往招聘平台复制岗位名称)'
              slotProps={{
                formHelperText: {
                  sx: { color: "error.main", fontSize: 14, fontWeight: "bold" },
                },
              }}
            />
            <TextField
              label='标签'
              value={form.label}
              onChange={(event) =>
                setForm({ ...form, label: event.target.value.slice(0, 20) })
              }
              fullWidth
              placeholder='例如：主力岗位、测试岗'
              helperText='选填，最多 20 字，用于在岗位列表区分同名岗位。'
              sx={{ mt: 2 }}
            />
            <Stack direction={{ xs: "column", sm: "row" }} spacing={2} sx={{ mt: 2, alignItems: { sm: "flex-start" } }}>
              <TextField
                label='每次打招呼上限'
                type='number'
                value={form.match_limit}
                onChange={(event) => setForm({ ...form, match_limit: Number(event.target.value || 0) })}
                slotProps={{ htmlInput: { min: 1 } }}
                sx={{ width: { sm: 220 } }}
              />
              <PositionSwitchOption
                checked={form.enable_sound}
                label='打招呼成功提示音'
                description='开启后，每次成功打招呼会播放提示音，不用一直盯着页面。'
                onChange={(checked) => setForm({ ...form, enable_sound: checked })}
              />
              <PositionSwitchOption
                checked={form.enable_thinking}
                label='思考模式'
                description='开启后 AI 会进行更深入的分析，判断通常更准确；但处理时间更久，AI 消耗也更高。'
                onChange={(checked) => setForm({ ...form, enable_thinking: checked })}
              />
            </Stack>
            <Divider sx={{ my: 1 }} />
            <ChoiceCards
              label={form.id ? "招聘平台（编辑岗位时不可更换）" : "招聘平台"}
              value={form.platform_id}
              columns={3}
              autoWidth
              onChange={(value) => selectPlatform(String(value))}
              options={[
                {
                  value: "boss",
                  label: "Boss直聘",
                  disabled:
                    Boolean(form.id) || !isPlatformOpen(platformConfigs, "boss"),
                  description: isPlatformOpen(platformConfigs, "boss")
                    ? "支持 OCR 和 AI 详情识别。"
                    : "暂未开放",
                  iconSrc: platformIconSrc("boss"),
                },
                {
                  value: "zhaopin",
                  label: "智联招聘",
                  disabled:
                    Boolean(form.id) ||
                    !isPlatformOpen(platformConfigs, "zhaopin"),
                  description: isPlatformOpen(platformConfigs, "zhaopin")
                    ? "支持 DOM 详情识别。"
                    : "暂未开放",
                  iconSrc: platformIconSrc("zhaopin"),
                },
                {
                  value: "hliepin",
                  label: "猎聘猎头端",
                  disabled:
                    Boolean(form.id) ||
                    !isPlatformOpen(platformConfigs, "hliepin"),
                  description: isPlatformOpen(platformConfigs, "hliepin")
                    ? "支持 DOM 详情识别。"
                    : "暂未开放",
                  iconSrc: platformIconSrc("hliepin"),
                },
                {
                  value: "liepin",
                  label: "猎聘企业端",
                  disabled:
                    Boolean(form.id) ||
                    !isPlatformOpen(platformConfigs, "liepin"),
                  description: isPlatformOpen(platformConfigs, "liepin")
                    ? "支持 DOM 详情识别。"
                    : "暂未开放",
                  iconSrc: platformIconSrc("liepin"),
                },
              ]}
            />
          </Box>
          <Divider />
          <Box>
            <Typography
              component='h3'
              sx={{ mb: 1.5, fontSize: 17, fontWeight: 780 }}
            >
              筛选方式
            </Typography>
            <Typography sx={{ mb: 1.5, color: "text.secondary", fontSize: 13 }}>
              选择候选人初筛方式，决定系统如何判断候选人是否符合岗位要求。
            </Typography>
            <ChoiceCards
              label='基础筛选模式'
              value={form.mode_default}
              onChange={(value) => void selectMode(String(value))}
              options={[
                {
                  value: "keyword",
                  label: "关键词筛选",
                  description: "按关键词和排除词判断，永久免费且速度快。",
                },
                {
                  value: "ai",
                  label: "AI 筛选（会员功能）",
                  description: "AI 先根据基础信息判断是否值得打开详情。",
                  memberOnly: true,
                },
              ]}
            />
          {form.mode_default === "keyword" ? (
            <>
              <Divider />
              <Box>
                <Typography
                  component='h3'
                  sx={{ mb: 1.5, fontSize: 17, fontWeight: 780 }}
                >
                  关键词筛选
                </Typography>
                <Stack spacing={2}>
                  <ChoiceCards
                    label='匹配方式'
                    value={form.is_and_mode}
                    onChange={(value) =>
                      setForm({ ...form, is_and_mode: Boolean(value) })
                    }
                    options={[
                      {
                        value: false,
                        label: "满足任一关键词",
                        description: "命中一个关键词即可通过，适合放宽筛选。",
                      },
                      {
                        value: true,
                        label: "必须同时满足",
                        description: "需要命中全部关键词，适合严格筛选。",
                      },
                    ]}
                  />
                  <Typography sx={{ color: "text.secondary", fontSize: 13 }}>
                    关键词模式是否打开详情，由“个人配置”中的详情查看概率控制。满足任一关键词更宽松，必须同时满足则更严格。
                  </Typography>
                  <Box
                    sx={{
                      display: "grid",
                      gridTemplateColumns: { xs: "1fr", md: "1fr 1fr" },
                      gap: 2,
                    }}
                  >
                    <TextField
                      label='关键词'
                      value={form.keywords}
                      onChange={(event) =>
                        setForm({ ...form, keywords: event.target.value })
                      }
                      multiline
                      minRows={3}
                      helperText='支持空格、中文逗号、英文逗号或换行分隔。'
                    />
                    <TextField
                      label='排除词'
                      value={form.exclude_keywords}
                      onChange={(event) =>
                        setForm({
                          ...form,
                          exclude_keywords: event.target.value,
                        })
                      }
                      multiline
                      minRows={3}
                      helperText='命中排除词后直接跳过。'
                    />
                  </Box>
                  {form.platform_id === "hliepin" ? (
                    <Stack spacing={1.25}>
                      <HLiepinHiddenCandidateFilters
                        hideViewed={form.hliepin_hide_viewed}
                        hideContacted={form.hliepin_hide_contacted}
                        hideContactObtained={
                          form.hliepin_hide_contact_obtained
                        }
                        onChange={(field, checked) =>
                          setForm({ ...form, [field]: checked })
                        }
                      />
                      <TextField
                        label='猎聘快捷搜索名'
                        value={form.hliepin_shortcut_search_name}
                        onChange={(event) =>
                          setForm({
                            ...form,
                            hliepin_shortcut_search_name: event.target.value,
                          })
                        }
                        fullWidth
                        placeholder='请填写猎聘搜索页已保存的快捷搜索名称'
                        helperText='填写后，岗位运行会直接选择猎聘页面中完全同名的快捷搜索，不再输入搜索关键词；如果不填，则使用正在发布的岗位进行匹配。'
                      />
                      <HLiepinShortcutSearchGuide
                        visible={Boolean(
                          form.hliepin_shortcut_search_name.trim(),
                        )}
                      />
                    </Stack>
                  ) : null}
                </Stack>
              </Box>
            </>
          ) : null}
          {form.mode_default === "ai" ? (
            <>
              <Divider />
              <Box>
                <Stack
                  direction={{ xs: "column", sm: "row" }}
                  sx={{ mb: 1.5, justifyContent: "space-between", gap: 1 }}
                >
                  <Box>
                    <Typography
                      component='h3'
                      sx={{ fontSize: 17, fontWeight: 780 }}
                    >
                      AI 筛选设置
                    </Typography>
                    <Typography
                      sx={{ mt: 0.5, color: "text.secondary", fontSize: 13 }}
                    >
                      请将 JD 岗位要求复制到"岗位要求"中，点击"AI
                      优化岗位要求"按钮，AI 会自动优化。
                    </Typography>
                  </Box>
                  <Button
                    startIcon={
                      optimizing ? (
                        <CircularProgress size={16} color='inherit' />
                      ) : (
                        <AutoFixHighRoundedIcon />
                      )
                    }
                    disabled={optimizing || !form.position_requirement.trim()}
                    onClick={() => void optimizeRequirement()}
                  >
                    {optimizing ? "分析中..." : "AI 优化岗位要求"}
                  </Button>
                </Stack>
                <Stack spacing={2}>
                  <TextField
                    label='岗位要求'
                    value={form.position_requirement}
                    onChange={(event) =>
                      setForm({
                        ...form,
                        position_requirement: event.target.value,
                      })
                    }
                    multiline
                    fullWidth
                    placeholder='必须有3年以上教学经验，必须有教师资格证，学历年龄 等基础条件可以在平台提前筛选好，更不要写跟岗位要求无关的 比如 岗位福利，工作环境等。'
                    minRows={7}
                    helperText='建议写清学历、经验、技能、行业、城市、到岗状态和明确的淘汰条件；不要填写“有上进心”等无法从简历判断的内容。'
                  />
                  {form.platform_id === "hliepin" ? (
                    <Stack spacing={1.25}>
                      <HLiepinHiddenCandidateFilters
                        hideViewed={form.hliepin_hide_viewed}
                        hideContacted={form.hliepin_hide_contacted}
                        hideContactObtained={
                          form.hliepin_hide_contact_obtained
                        }
                        onChange={(field, checked) =>
                          setForm({ ...form, [field]: checked })
                        }
                      />
                      <TextField
                        label='猎聘快捷搜索名'
                        value={form.hliepin_shortcut_search_name}
                        onChange={(event) =>
                          setForm({
                            ...form,
                            hliepin_shortcut_search_name: event.target.value,
                          })
                        }
                        fullWidth
                        placeholder='请填写猎聘搜索页已保存的快捷搜索名称'
                        helperText='填写后，岗位运行会直接选择猎聘页面中完全同名的快捷搜索，不再输入搜索关键词；如果不填，则使用正在发布的岗位进行匹配。它不参与本地简历的 AI 判断。'
                      />
                      <HLiepinShortcutSearchGuide
                        visible={Boolean(
                          form.hliepin_shortcut_search_name.trim(),
                        )}
                      />
                    </Stack>
                  ) : null}
                  <Box
                    sx={{
                      border: "1px solid",
                      borderColor: "divider",
                      borderRadius: "8px",
                      overflow: "hidden",
                    }}
                  >
                    <Button
                      fullWidth
                      onClick={() => setAdvancedOpen((value) => !value)}
                      endIcon={
                        <ExpandMoreRoundedIcon
                          sx={{
                            transform: advancedOpen
                              ? "rotate(180deg)"
                              : "rotate(0deg)",
                            transition: "transform .18s ease",
                          }}
                        />
                      }
                      sx={{
                        justifyContent: "space-between",
                        px: 1.5,
                        py: 1.25,
                        color: "text.primary",
                        bgcolor: advancedOpen ? "action.hover" : "transparent",
                      }}
                    >
                      高级设置
                    </Button>
                    <Box sx={{ px: 1.5, pb: advancedOpen ? 1.5 : 1.25 }}>
                      <Typography
                        sx={{
                          color: "text.secondary",
                          fontSize: 13,
                          lineHeight: 1.75,
                        }}
                      >
                        这里是增加 AI 准确率的各项设置。
                      </Typography>
                    </Box>
                    <Collapse in={advancedOpen} unmountOnExit>
                      <Stack spacing={2} sx={{ px: 1.5, pb: 1.5 }}>
                        <PromptField
                          label='打开详情提示词（一般不需要修改）'
                          value={form.open_detail_prompt}
                          defaultValue={defaults.open_detail_prompt}
                          description='只用于第一次分析，判断候选人是否值得打开详情。普通岗位可以宽松一些，高级岗位可以更严格。'
                          onChange={(value) =>
                            setForm({ ...form, open_detail_prompt: value })
                          }
                        />
                        <TextField
                          label='看详情阈值分'
                          type='number'
                          value={form.detail_score_threshold}
                          onChange={(event) =>
                            setForm({
                              ...form,
                              detail_score_threshold: Number(
                                event.target.value,
                              ),
                            })
                          }
                          slotProps={{ htmlInput: { min: 0, max: 100 } }}
                          helperText='首次评分大于等于该值时打开候选人详情。'
                        />
                        <ChoiceCards
                          label='是否生成简历'
                          value={form.output_structured_resume}
                          onChange={(value) =>
                            setForm({
                              ...form,
                              output_structured_resume: Boolean(value),
                            })
                          }
                          options={[
                            {
                              value: false,
                              label: "不需要",
                              description: "AI消耗少、不会存简历。",
                            },
                            {
                              value: true,
                              label: "需要",
                              description: "AI消耗多，会把信息放到简历库里。",
                            },
                          ]}
                        />
                        <PromptField
                          label='打招呼提示词（一般不需要修改）'
                          value={form.filter_prompt}
                          defaultValue={defaults.filter_prompt}
                          description='用于详情分析并决定候选人的最终分数，直接影响是否执行打招呼。'
                          onChange={(value) =>
                            setForm({ ...form, filter_prompt: value })
                          }
                        />
                        <TextField
                          label='打招呼阈值分'
                          type='number'
                          value={form.greet_score_threshold}
                          onChange={(event) => {
                            const nextThreshold = Number(event.target.value);
                            setForm({
                              ...form,
                              greet_score_threshold: nextThreshold,
                              request_score_threshold:
                                form.request_score_threshold ===
                                form.greet_score_threshold
                                  ? nextThreshold
                                  : form.request_score_threshold,
                            });
                          }}
                          slotProps={{ htmlInput: { min: 0, max: 100 } }}
                          helperText='详情评分大于等于该值时执行打招呼。'
                        />
                      </Stack>
                    </Collapse>
                  </Box>
                </Stack>
              </Box>
            </>
          ) : null}
          </Box>
          {form.id ? (
            <>
              <Divider />
              <Box>
                <Typography
                  component='h3'
                  sx={{ mb: 1.5, fontSize: 17, fontWeight: 780 }}
                >
                  详情判断
                </Typography>
                <Typography sx={{ mb: 1.5, color: "text.secondary", fontSize: 13 }}>
                  选择哪种详情方式就只使用哪一种：DOM 最快，OCR
                在本地识别截图文字，AI 能理解完整页面但耗时更长。
              </Typography>
              <ChoiceCards
                label='详情信息筛选模式  (决定是否打招呼)'
                value={form.detail_mode}
                columns={3}
                onChange={(value) => void selectDetailMode(String(value))}
                options={[
                  {
                    value: "dom",
                    label: "DOM 识别",
                    description: "BOSS直聘不支持DOM识别，速度快，精度高，免费",
                    disabled: form.platform_id === "boss",
                  },
                  {
                    value: "ocr",
                    label: "OCR 识别",
                    description:
                      "离线识别截图文字，速度快。电脑配置低就别选这个。",
                    disabled: isDOMOnlyPlatform(form.platform_id),
                  },
                  {
                    value: "ai",
                    label: "AI 识别（会员功能）",
                    description: "直接理解完整详情截图，效果最好但更慢。",
                    disabled: isDOMOnlyPlatform(form.platform_id),
                    memberOnly: true,
                  },
                ]}
              />
            </Box>
            </>
          ) : null}
          <Divider />
          <Box>
            <Typography
              component='h3'
              sx={{ mb: 1.5, fontSize: 17, fontWeight: 780 }}
            >
              打招呼
            </Typography>
            <Typography sx={{ mb: 1.5, color: "text.secondary", fontSize: 13 }}>
              候选人通过筛选后发送的消息和后续动作。
            </Typography>
            <Stack spacing={2}>
              <TextField
                label='首次打招呼语（可选）'
                value={form.greet_message}
                onChange={(event) =>
                  setForm({ ...form, greet_message: event.target.value })
                }
                multiline
                minRows={3}
              />
              <Box>
                <Typography sx={{ mb: 0.5, fontSize: 14, fontWeight: 760 }}>
                  打招呼后自动索要
                </Typography>
                <Stack
                  direction='row'
                  spacing={1.5}
                  sx={{ flexWrap: "wrap", columnGap: 1.5 }}
                >
                  <FormControlLabel
                    control={
                      <Checkbox
                        size='small'
                        checked={form.request_resume}
                        disabled={!canUseAutoReply(subscription)}
                        onChange={(event) =>
                          setForm({ ...form, request_resume: event.target.checked })
                        }
                      />
                    }
                    label={
                      <Stack direction='row' spacing={0.75} sx={{ alignItems: 'center' }}>
                        <span>索要简历</span>
                        <Chip
                          size='small'
                          label='PRO 用户专享功能'
                          sx={{
                            height: 20,
                            fontSize: 11,
                            fontWeight: 600,
                            color: '#fff',
                            backgroundColor: 'primary.main',
                            '& .MuiChip-label': { px: 0.75 },
                          }}
                        />
                      </Stack>
                    }
                  />
                </Stack>
                <Typography sx={{ color: "text.secondary", fontSize: 12.5 }}>
                  当前智联招聘和猎聘猎头端已实现；只有最终 AI
                  评分严格大于索要分数时，才会执行已勾选的索要项。
                </Typography>
                <TextField
                  label='索要分数'
                  type='number'
                  value={form.request_score_threshold}
                  onChange={(event) =>
                    setForm({
                      ...form,
                      request_score_threshold: Number(event.target.value),
                    })
                  }
                  slotProps={{ htmlInput: { min: 0, max: 100 } }}
                  helperText='候选人最终 AI 评分必须严格大于该值才执行索要；没有 AI 评分时不会索要。默认与打招呼阈值分相同。'
                  sx={{ mt: 1.25, width: { xs: "100%", sm: 360 } }}
                />
              </Box>
            </Stack>
          </Box>
          <Divider />
          <Box>
            <Stack direction='row' spacing={1} sx={{ alignItems: 'center', mb: 1.5 }}>
              <Typography
                component='h3'
                sx={{ fontSize: 17, fontWeight: 780 }}
              >
                AI 自动回复
              </Typography>
              <Chip
                size='small'
                label='PRO 用户专享功能'
                sx={{
                  height: 22,
                  fontSize: 12,
                  fontWeight: 600,
                  color: '#fff',
                  backgroundColor: 'primary.main',
                  '& .MuiChip-label': { px: 1 },
                }}
              />
            </Stack>
            <Typography sx={{ mb: 1.5, color: "text.secondary", fontSize: 13 }}>
              候选人发消息过来时，AI 根据以下配置自动回复。
            </Typography>
            <Stack spacing={2} sx={{ opacity: canUseAutoReply(subscription) ? 1 : 0.55, pointerEvents: canUseAutoReply(subscription) ? 'auto' : 'none' }}>
              <PromptField
                label='AI 回复提示词（可选）'
                value={form.reply_prompt}
                defaultValue=''
                defaultActionLabel='清空'
                emptyPlaceholder='可留空，使用系统默认的回复规则'
                description='岗位开启 AI 自动回复时使用；写清回复语气、重点和禁忌，留空则按默认规则回复。'
                onChange={(value) =>
                  setForm({ ...form, reply_prompt: value })
                }
                disabled={!canUseAutoReply(subscription)}
              />
              <Box>
                <Typography sx={{ mb: 0.5, fontSize: 14, fontWeight: 600 }}>
                  常见问答语料（可选，最多 10 条）
                </Typography>
                <Typography sx={{ mb: 1, fontSize: 12, color: "text.secondary" }}>
                  候选人可能问到的问题和标准答案，AI 回复时会参考这些内容。
                </Typography>
                <Stack spacing={1}>
                  {form.reply_faq.map((entry, index) => (
                    <Stack key={index} direction='row' spacing={1} sx={{ alignItems: "flex-start" }}>
                      <TextField
                        size='small'
                        label={`问题 ${index + 1}`}
                        value={entry.q}
                        placeholder='如：上下班时间'
                        slotProps={{ htmlInput: { maxLength: 20 } }}
                        helperText={`${entry.q.length}/20`}
                        sx={{ flex: 2 }}
                        disabled={!canUseAutoReply(subscription)}
                        onChange={(e) => {
                          const next = [...form.reply_faq];
                          next[index] = { ...next[index], q: e.target.value.slice(0, 20) };
                          setForm({ ...form, reply_faq: next });
                        }}
                      />
                      <TextField
                        size='small'
                        label={`回答 ${index + 1}`}
                        value={entry.a}
                        placeholder='如：9:00-18:00'
                        slotProps={{ htmlInput: { maxLength: 50 } }}
                        helperText={`${entry.a.length}/50`}
                        sx={{ flex: 3 }}
                        disabled={!canUseAutoReply(subscription)}
                        onChange={(e) => {
                          const next = [...form.reply_faq];
                          next[index] = { ...next[index], a: e.target.value.slice(0, 50) };
                          setForm({ ...form, reply_faq: next });
                        }}
                      />
                      <IconButton
                        size='small'
                        sx={{ mt: 0.5 }}
                        disabled={!canUseAutoReply(subscription)}
                        onClick={() => {
                          const next = form.reply_faq.filter((_, i) => i !== index);
                          setForm({ ...form, reply_faq: next });
                        }}
                      >
                        <DeleteOutlineRoundedIcon fontSize='small' />
                      </IconButton>
                    </Stack>
                  ))}
                  {form.reply_faq.length < 10 && (
                    <Button
                      size='small'
                      startIcon={<AddRoundedIcon />}
                      disabled={!canUseAutoReply(subscription)}
                      onClick={() =>
                        setForm({
                          ...form,
                          reply_faq: [...form.reply_faq, { q: "", a: "" }],
                        })
                      }
                    >
                      添加问答
                    </Button>
                  )}
                </Stack>
              </Box>
              <TextField
                multiline
                minRows={2}
                maxRows={4}
                label='拒绝话术（可选）'
                value={form.reply_reject_template}
                placeholder='感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。'
                helperText='候选人不符合岗位要求时发送此消息，留空使用系统默认。'
                disabled={!canUseAutoReply(subscription)}
                onChange={(e) =>
                  setForm({ ...form, reply_reject_template: e.target.value.slice(0, 200) })
                }
              />
            </Stack>
          </Box>
          <Divider />
          <Box>
            <Stack direction='row' spacing={1} sx={{ alignItems: 'center', mb: 1.5 }}>
              <Typography
                component='h3'
                sx={{ fontSize: 17, fontWeight: 780 }}
              >
                复打招呼
              </Typography>
              <Chip
                size='small'
                label='PRO 用户专享功能'
                sx={{
                  height: 22,
                  fontSize: 12,
                  fontWeight: 600,
                  color: '#fff',
                  backgroundColor: 'primary.main',
                  '& .MuiChip-label': { px: 1 },
                }}
              />
            </Stack>
            <Typography sx={{ mb: 1.5, color: "text.secondary", fontSize: 13 }}>
              对之前打过招呼但没回复的候选人再发一次消息，提高转化率。
            </Typography>
            <Stack spacing={2} sx={{ opacity: canUseAutoReply(subscription) ? 1 : 0.55, pointerEvents: canUseAutoReply(subscription) ? 'auto' : 'none' }}>
              <PromptField
                label='复打招呼提示词（可选）'
                value={form.re_greet_prompt}
                defaultValue=''
                defaultActionLabel='清空'
                emptyPlaceholder='可留空，使用系统默认的复打规则'
                description='告诉 AI 如何生成复打消息，比如语气、重点、禁忌；留空则按默认规则生成。'
                onChange={(value) =>
                  setForm({ ...form, re_greet_prompt: value })
                }
                disabled={!canUseAutoReply(subscription)}
              />
              <FormControlLabel
                control={
                  <Checkbox
                    checked={form.re_greet_skip_refused}
                    onChange={(e) =>
                      setForm({ ...form, re_greet_skip_refused: e.target.checked })
                    }
                    disabled={!canUseAutoReply(subscription)}
                  />
                }
                label='候选人明确拒绝时不再复打'
              />
            </Stack>
          </Box>
          {!aiMembership &&
          (form.mode_default === "ai" ||
            (form.id && form.detail_mode === "ai")) ? (
            <Alert severity='warning'>
              当前会员已到期，AI 选项无法保存。可以改为关键词筛选和 OCR 识别。
            </Alert>
          ) : null}
        </Stack>
      </AdminDialog>
    </>
  );
}

/** PromptField 输出带恢复系统默认按钮的提示词输入框。 */
function PromptField({
  label,
  value,
  defaultValue,
  defaultActionLabel = "设为系统默认",
  emptyPlaceholder = "系统暂未配置默认提示词",
  description,
  onChange,
  disabled = false,
}: {
  label: string;
  value: string;
  defaultValue: string;
  defaultActionLabel?: string;
  emptyPlaceholder?: string;
  description: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  return (
    <Box>
      <Stack
        direction='row'
        sx={{ mb: 0.75, justifyContent: "space-between", alignItems: "center" }}
      >
        <Typography sx={{ fontSize: 13, fontWeight: 700 }}>{label}</Typography>
        <Button
          size='small'
          startIcon={<RestartAltRoundedIcon />}
          onClick={() => onChange(defaultValue)}
          disabled={disabled}
        >
          {defaultActionLabel}
        </Button>
      </Stack>
      <TextField
        value={value}
        onChange={(event) => onChange(event.target.value)}
        multiline
        minRows={6}
        fullWidth
        disabled={disabled}
        placeholder={defaultValue ? "已加载系统默认提示词" : emptyPlaceholder}
      />
      <Typography
        sx={{
          mt: 0.75,
          color: "text.secondary",
          fontSize: 12.5,
          lineHeight: 1.6,
        }}
      >
        {description}
      </Typography>
    </Box>
  );
}

/** HLiepinHiddenCandidateFilters 配置猎聘搜索页需要自动勾选的候选人隐藏条件。 */
function HLiepinHiddenCandidateFilters({
  hideViewed,
  hideContacted,
  hideContactObtained,
  onChange,
}: {
  hideViewed: boolean;
  hideContacted: boolean;
  hideContactObtained: boolean;
  onChange: (
    field:
      | "hliepin_hide_viewed"
      | "hliepin_hide_contacted"
      | "hliepin_hide_contact_obtained",
    checked: boolean,
  ) => void;
}) {
  return (
    <Box
      sx={{
        px: 1.5,
        py: 1.25,
        border: "1px solid",
        borderColor: "divider",
        borderRadius: "8px",
        bgcolor: "action.hover",
      }}
    >
      <Typography sx={{ fontSize: 14, fontWeight: 760 }}>
        猎聘候选人隐藏条件
      </Typography>
      <Stack
        direction='row'
        spacing={1.5}
        sx={{ mt: 0.25, flexWrap: "wrap", columnGap: 1.5 }}
      >
        <FormControlLabel
          control={
            <Checkbox
              size='small'
              checked={hideViewed}
              onChange={(event) =>
                onChange("hliepin_hide_viewed", event.target.checked)
              }
            />
          }
          label='隐藏已查看'
        />
        <FormControlLabel
          control={
            <Checkbox
              size='small'
              checked={hideContacted}
              onChange={(event) =>
                onChange("hliepin_hide_contacted", event.target.checked)
              }
            />
          }
          label='隐藏已沟通'
        />
        <FormControlLabel
          control={
            <Checkbox
              size='small'
              checked={hideContactObtained}
              onChange={(event) =>
                onChange(
                  "hliepin_hide_contact_obtained",
                  event.target.checked,
                )
              }
            />
          }
          label='隐藏已获取联系方式'
        />
      </Stack>
      <Typography sx={{ color: "text.secondary", fontSize: 12.5 }}>
        岗位运行时只会勾选这里启用的条件；旧岗位默认三项全部启用。
      </Typography>
    </Box>
  );
}

/** HLiepinShortcutSearchGuide 在填写猎聘快捷搜索名后展示配置提醒和可放大教程图。 */
function HLiepinShortcutSearchGuide({ visible }: { visible: boolean }) {
  return (
    <Collapse in={visible} unmountOnExit>
      <Box
        sx={{
          p: 1.5,
          border: "1px solid",
          borderColor: "warning.light",
          borderRadius: "8px",
          bgcolor: "#fffaf0",
        }}
      >
        <Typography sx={{ fontSize: 14, fontWeight: 780 }}>
          请先在猎聘创建并保存快捷搜索
        </Typography>
        <Alert severity='warning' sx={{ mt: 1, mb: 1.5 }}>
          <Typography sx={{ fontSize: 13, lineHeight: 1.7 }}>
            请先在猎聘搜索页面配置关键词和全部筛选条件，点击“保存条件”创建快捷搜索，再把保存后的名称完整填写到上方“猎聘快捷搜索名”。岗位运行会直接使用该快捷搜索包含的全部条件，不会再次填写搜索关键词。
          </Typography>
          <Typography sx={{ mt: 0.75, fontSize: 13, lineHeight: 1.7 }}>
            填写的名称必须与猎聘页面显示的快捷搜索名完全一致，否则岗位运行会停止并说明未找到。不同岗位请使用容易区分且不重复的快捷搜索名，避免选错筛选条件。
          </Typography>
        </Alert>
        <ClickableImagePreview
          src={HLIEPIN_SHORTCUT_GUIDE_IMAGE_SRC}
          alt='猎聘保存搜索条件并创建快捷搜索教程'
          hint='点击图片放大查看猎聘快捷搜索创建步骤'
        />
      </Box>
    </Collapse>
  );
}

/** createEmptyForm 返回免费版可用的岗位默认表单。 */
function createEmptyForm() {
  return {
    id: "",
    name: "",
    label: "",
    platform_id: "boss",
    mode_default: "keyword",
    detail_mode: "ocr",
    keywords: "",
    exclude_keywords: "",
    is_and_mode: false,
    position_requirement: "",
    hliepin_shortcut_search_name: "",
    hliepin_hide_viewed: true,
    hliepin_hide_contacted: true,
    hliepin_hide_contact_obtained: true,
    open_detail_prompt: "",
    filter_prompt: "",
    review_prompt: "",
    reply_prompt: "",
    reply_faq: [] as { q: string; a: string }[],
    reply_reject_template: "",
    re_greet_prompt: "",
    re_greet_skip_refused: false,
    detail_score_threshold: 60,
    greet_score_threshold: 70,
    request_score_threshold: 70,
    output_structured_resume: false,
    request_phone: false,
    request_wechat: false,
    request_resume: false,
    greet_message: "",
    description: "",
    match_limit: 50,
    enable_sound: true,
    enable_thinking: false,
  };
}

/** firstOpenPlatformID 返回第一个已经开放的招聘平台。 */
function firstOpenPlatformID(configs: PlatformConfigLike[]) {
  return PLATFORM_OPEN_ORDER.find((platformID) =>
    isPlatformOpen(configs, platformID),
  );
}

/** formFromItem 将后端岗位数据转换为编辑表单。 */
function formFromItem(
  item: any,
  defaults: ReturnType<typeof normalizePrompts>,
): PositionForm {
  const common = item.common_config || {};
  const ai = item.ai_config || {};
  return fillPrompts(
    {
      id: item.id || "",
      name: item.name || "",
      label: item.label || "",
      platform_id: item.platform_id || "boss",
      mode_default: common.mode_default || "keyword",
      detail_mode: normalizeDetailMode(
        item.platform_id,
        common.detail_mode || "ocr",
      ),
      output_structured_resume: Boolean(common.output_structured_resume),
      request_phone: Boolean(common.request_phone),
      request_wechat: Boolean(common.request_wechat),
      request_resume: Boolean(common.request_resume),
      keywords: (item.keywords || []).join(" "),
      exclude_keywords: (item.exclude_keywords || []).join(" "),
      is_and_mode: Boolean(item.is_and_mode),
      position_requirement: ai.position_requirement || "",
      hliepin_shortcut_search_name:
        common.hliepin_shortcut_search_name || "",
      hliepin_hide_viewed: common.hliepin_hide_viewed !== false,
      hliepin_hide_contacted: common.hliepin_hide_contacted !== false,
      hliepin_hide_contact_obtained:
        common.hliepin_hide_contact_obtained !== false,
      open_detail_prompt: normalizePrompt(ai.open_detail_prompt),
      filter_prompt: normalizePrompt(
        ai.greet_prompt || ai.filter_prompt || ai.click_prompt,
      ),
      review_prompt: normalizePrompt(ai.review_prompt),
      reply_prompt: normalizePrompt(ai.reply_prompt),
      reply_faq: normalizeFAQList(ai.reply_faq),
      reply_reject_template: String(ai.reply_reject_template || ""),
      re_greet_prompt: normalizePrompt(ai.re_greet_prompt),
      re_greet_skip_refused: Boolean(ai.re_greet_skip_refused),
      detail_score_threshold: Number(ai.detail_score_threshold ?? 60),
      greet_score_threshold: Number(ai.greet_score_threshold ?? 70),
      request_score_threshold: Number(
        ai.request_score_threshold ?? ai.greet_score_threshold ?? 70,
      ),
      greet_message: item.greet_message || "",
      description: item.description || "",
      match_limit: Number(item.match_limit ?? 50),
      enable_sound: Boolean(item.enable_sound),
      enable_thinking: Boolean(item.enable_thinking),
    },
    defaults,
  );
}

/** isCurrentUserPosition 判断岗位是否由当前登录账号创建。 */
function isCurrentUserPosition(item: any, currentEmail: unknown) {
  if (typeof item?.is_current_user === "boolean") return item.is_current_user;
  const creator = String(item?.creator_email || "").trim().toLowerCase();
  const current = String(currentEmail || "").trim().toLowerCase();
  return !creator || (Boolean(current) && creator === current);
}

/** sortTeamPositions 按当前账号优先、创建时间从新到旧排列团队岗位。 */
function sortTeamPositions(items: any[], currentEmail: unknown) {
  return [...items].sort((left, right) => {
    const ownDifference = Number(isCurrentUserPosition(right, currentEmail)) - Number(isCurrentUserPosition(left, currentEmail));
    if (ownDifference !== 0) return ownDifference;
    const leftCreatedAt = new Date(left?.created_at || 0).getTime();
    const rightCreatedAt = new Date(right?.created_at || 0).getTime();
    return rightCreatedAt - leftCreatedAt;
  });
}

/** fillPrompts 为岗位空提示词补充系统默认值。 */
function fillPrompts(
  form: PositionForm,
  defaults: ReturnType<typeof normalizePrompts>,
) {
  return {
    ...form,
    open_detail_prompt: form.open_detail_prompt || defaults.open_detail_prompt,
    filter_prompt: form.filter_prompt || defaults.filter_prompt,
    review_prompt: form.review_prompt || "",
  };
}

/** normalizePrompts 统一系统默认提示词字段。 */
function normalizePrompts(value: any) {
  return {
    filter_prompt: normalizePrompt(value?.filter_prompt),
    open_detail_prompt: normalizePrompt(value?.open_detail_prompt),
    review_prompt: normalizePrompt(value?.review_prompt),
  };
}

/** normalizePrompt 还原历史数据中的字面换行。 */
function normalizePrompt(value: unknown) {
  return String(value || "").replace(/\\n/g, "\n");
}

/** normalizeDetailMode 修正平台不支持的详情模式。 */
function normalizeDetailMode(platformID: string, mode: string) {
  if (isDOMOnlyPlatform(platformID)) return "dom";
  if (platformID === "boss" && mode === "dom") return "ocr";
  return ["dom", "ocr", "ai"].includes(mode) ? mode : "ocr";
}

/** defaultCreateDetailMode 返回新增岗位时自动使用的详情识别模式。 */
function defaultCreateDetailMode(platformID: string, memberActive: boolean) {
  if (platformID === "boss") return memberActive ? "ai" : "ocr";
  return "dom";
}

/** defaultCreateMode 返回新增岗位时自动使用的基础筛选模式。 */
function defaultCreateMode(memberActive: boolean) {
  return memberActive ? "ai" : "keyword";
}

/** isDOMOnlyPlatform 判断平台是否只支持 DOM 详情识别。 */
function isDOMOnlyPlatform(platformID: string) {
  return ["hliepin", "liepin", "zhaopin"].includes(platformID);
}

/** splitKeywords 将多种分隔符转换成忽略大小写的去重关键词数组。 */
function splitKeywords(value: string) {
  const seen = new Set<string>();
  return String(value || "")
    .split(/[,\s，、；;]+/)
    .map((item) => item.trim())
    .filter((item) => {
      const key = item.toLowerCase();
      if (!item || seen.has(key)) return false;
      seen.add(key);
      return true;
    });
}

/** detailModeLabel 返回详情模式中文名称。 */
function detailModeLabel(value: string) {
  return value === "dom" ? "DOM识别" : value === "ai" ? "AI识别" : "OCR识别";
}

/** normalizePositionTaskStats 把本地最近任务统计转换成安全的非负整数。 */
function normalizePositionTaskStats(task: any): PositionTaskStats {
  return {
    scanned_count: Math.max(0, Number(task?.scanned_count) || 0),
    greeted_count: Math.max(0, Number(task?.greeted_count) || 0),
    skipped_count: Math.max(0, Number(task?.skipped_count) || 0),
  };
}

/** normalizeFloatingAnalysis 校验本地程序返回的 AI 或关键词结构化状态。 */
function normalizeFloatingAnalysis(value: unknown): PositionAnalysisStatus | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const kind = record.kind === "ai" || record.kind === "keyword"
    ? record.kind
    : null;
  const phase =
    record.phase === "loading" ||
    record.phase === "result" ||
    record.phase === "error"
      ? record.phase
      : null;
  if (!kind || !phase) return null;
  const status: PositionAnalysisStatus = {
    kind,
    phase,
    stage:
      record.stage === "preview" || record.stage === "final"
        ? record.stage
        : undefined,
    terminal: Boolean(record.terminal),
    candidate_name: String(record.candidate_name || "").trim(),
    reason: String(record.reason || "").trim(),
    updated_at: String(record.updated_at || "").trim(),
    keywords: floatingStringArray(record.keywords),
    matched_keywords: floatingStringArray(record.matched_keywords),
    exclude_keywords: floatingStringArray(record.exclude_keywords),
    matched_excludes: floatingStringArray(record.matched_excludes),
  };
  if (typeof record.score === "number" && Number.isFinite(record.score)) {
    status.score = record.score;
  }
  if (
    typeof record.threshold === "number" &&
    Number.isFinite(record.threshold)
  ) {
    status.threshold = record.threshold;
  }
  if (typeof record.accepted === "boolean") {
    status.accepted = record.accepted;
  }
  return status;
}

/** floatingStringArray 清理悬浮窗状态中的字符串数组。 */
function floatingStringArray(value: unknown) {
  if (!Array.isArray(value)) return [];
  return value
    .filter((item): item is string => typeof item === "string")
    .map((item) => item.trim())
    .filter(Boolean);
}

/** nextFloatingAnalysis 保证候选人最终结果至少展示八秒，不被下一位任何状态马上顶掉。 */
function nextFloatingAnalysis(
  current: PositionAnalysisStatus | null,
  incoming: PositionAnalysisStatus | null,
) {
  if (!incoming) return current;
  if (!current?.terminal || current.phase === "loading") {
    return incoming;
  }
  const currentTime = new Date(current.updated_at).getTime();
  if (
    current.updated_at !== incoming.updated_at &&
    Number.isFinite(currentTime) &&
    Date.now() - currentTime < 8000
  ) {
    return current;
  }
  return incoming;
}

/** PositionSwitchOption 展示岗位布尔开关及面向普通用户的通俗说明。 */
function PositionSwitchOption(props: {
  checked: boolean;
  label: string;
  description: string;
  onChange: (checked: boolean) => void;
}) {
  const { checked, label, description, onChange } = props;
  return (
    <Stack spacing={0.25} sx={{ width: { sm: 310 }, maxWidth: "100%" }}>
      <FormControlLabel
        sx={{ m: 0 }}
        control={<Switch checked={checked} onChange={(event) => onChange(event.target.checked)} />}
        label={label}
      />
      <Typography sx={{ pl: 6.25, color: "text.secondary", fontSize: 12.5, lineHeight: 1.55 }}>
        {description}
      </Typography>
    </Stack>
  );
}

/** positionStartReason 将岗位启动错误归一为后台可筛选的失败原因。 */
function positionStartReason(message: string) {
  const value = String(message || "").toLowerCase();
  if (value.includes("会员") || value.includes("订阅")) return "subscription_expired";
  if (value.includes("ai") || value.includes("模型") || value.includes("余额") || value.includes("key")) return "ai_config_invalid";
  if (value.includes("登录") || value.includes("cookie")) return "platform_not_logged_in";
  if (value.includes("组件") || value.includes("runtime") || value.includes("node")) return "runtime_missing";
  if (value.includes("版本")) return "agent_version_outdated";
  return "position_start_failed";
}

/** isPositionStartErrorStatus 判断启动弹框状态是否需要使用错误色提醒。 */
function isPositionStartErrorStatus(message: string) {
  return ["没登录", "失败", "没跑完", "余额不足", "版本过低", "订阅", "没开放", "缺少", "超时"]
    .some((keyword) => String(message || "").includes(keyword));
}

/** PositionLogPanel 渲染岗位最近日志和日志操作入口。 */
function PositionLogPanel(props: {
  logs: any[];
  loading: boolean;
  onRefresh: () => void;
  onViewAll: () => void;
  onClear: () => void;
}) {
  const { logs, loading, onRefresh, onViewAll, onClear } = props;
  return (
    <Box
      sx={{
        mt: 1.5,
        border: "1px solid",
        borderColor: "divider",
        borderRadius: "8px",
        bgcolor: "action.hover",
        overflow: "hidden",
      }}
    >
      <Stack
        direction={{ xs: "column", sm: "row" }}
        spacing={1}
        sx={{
          px: 1.5,
          py: 1,
          alignItems: { sm: "center" },
          justifyContent: "space-between",
          borderBottom: "1px solid",
          borderColor: "divider",
        }}
      >
        <Stack direction='row' spacing={1} sx={{ alignItems: "center", flexWrap: "wrap" }}>
          <Typography sx={{ fontSize: 13, fontWeight: 760 }}>
            本地岗位日志（最近 {LOG_LIMIT} 条）
          </Typography>
          <Button size='small' onClick={onViewAll}>查看全部日志</Button>
        </Stack>
        <Stack direction='row' spacing={0.5}>
          <Button size='small' onClick={onRefresh} disabled={loading}>
            {loading ? "刷新中" : "刷新"}
          </Button>
          <Button color='error' size='small' onClick={onClear}>
            清空
          </Button>
        </Stack>
      </Stack>
      <PositionLogList logs={logs} maxHeight={420} />
    </Box>
  );
}

/** PositionLogList 按时间从新到旧展示日志，打开后优先看到最新记录。 */
function PositionLogList(props: { logs: any[]; maxHeight: number | string }) {
  const { logs, maxHeight } = props;
  const orderedLogs = sortPositionLogsNewestFirst(logs);

  return (
    <Stack
      spacing={0}
      sx={{ p: 1, maxHeight, overflow: "auto" }}
    >
      {orderedLogs.length ? orderedLogs.map((item, index) => (
        <PositionLogLine
          key={String(item.id || `${item.created_at || item.time}-${index}`)}
          item={item}
          previous={index < orderedLogs.length - 1 ? orderedLogs[index + 1] : null}
        />
      )) : (
        <Typography sx={{ py: 4, color: "text.secondary", fontSize: 13, textAlign: "center" }}>
          暂无数据，开始运行后将自动记录。
        </Typography>
      )}
    </Stack>
  );
}

/** PositionLogLine 渲染单条带中文等级和耗时的岗位日志。 */
function PositionLogLine(props: { item: any; previous: any | null }) {
  const { item, previous } = props;
  const appearance = positionLogAppearance(item.level);
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: { xs: "1fr", md: "190px 82px 72px minmax(0, 1fr)" },
        gap: 1,
        py: 0.75,
        borderBottom: "1px solid",
        borderColor: appearance.error ? "#f0b4b4" : "divider",
        color: appearance.color,
      }}
    >
      <Typography sx={{ color: appearance.color, fontSize: 12 }}>
        {formatPositionLogTime(item.created_at || item.time)}
      </Typography>
      <Typography sx={{ color: appearance.color, fontSize: 12 }}>
        {positionLogDelta(item, previous)}
      </Typography>
      <Typography sx={{ color: appearance.color, fontSize: 12, fontWeight: appearance.weight }}>
        {appearance.label}
      </Typography>
      <Typography sx={{ color: appearance.color, fontSize: 13, fontWeight: appearance.weight, lineHeight: 1.65, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
        {positionLogMessage(item)}
      </Typography>
    </Box>
  );
}

/** sortPositionLogsNewestFirst 按时间从新到旧排列，让最新日志位于顶部。 */
function sortPositionLogsNewestFirst(logs: any[]) {
  return [...logs].sort((left, right) => positionLogTimeMs(right.created_at || right.time) - positionLogTimeMs(left.created_at || left.time));
}

/** positionLogMessage 提取兼容新旧结构的日志正文。 */
function positionLogMessage(item: any) {
  return String(item?.message || item?.msg || item?.detail || "");
}

/** positionLogTimeMs 将日志时间转换成用于排序的毫秒时间戳。 */
function positionLogTimeMs(value: unknown) {
  const time = new Date(String(value || "")).getTime();
  return Number.isNaN(time) ? 0 : time;
}

/** formatPositionLogTime 将日志时间格式化到毫秒。 */
function formatPositionLogTime(value: unknown) {
  const date = new Date(String(value || ""));
  if (Number.isNaN(date.getTime())) return "--";
  const pad = (input: number, size = 2) => String(input).padStart(size, "0");
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
}

/** positionLogDelta 返回当前日志与上一条日志的时间间隔。 */
function positionLogDelta(item: any, previous: any | null) {
  if (!previous) return "+0ms";
  const currentMs = positionLogTimeMs(item.created_at || item.time);
  const previousMs = positionLogTimeMs(previous.created_at || previous.time);
  if (!currentMs || !previousMs) return "+--ms";
  return `+${Math.max(0, currentMs - previousMs)}ms`;
}

/** buildPositionLogText 构建可复制的完整岗位日志文本。 */
function buildPositionLogText(logs: any[]) {
  const orderedLogs = sortPositionLogsNewestFirst(logs);
  return orderedLogs.map((item, index) => {
    const previous = index < orderedLogs.length - 1 ? orderedLogs[index + 1] : null;
    return `${formatPositionLogTime(item.created_at || item.time)} ${positionLogDelta(item, previous)} ${positionLogAppearance(item.level).label} ${positionLogMessage(item)}`;
  }).join("\n");
}

/** positionLogAppearance 返回岗位日志等级对应的中文分类和文字样式。 */
function positionLogAppearance(value: unknown) {
  const level = String(value || "info").trim().toLowerCase();
  if (level === "error") return { label: "错误", color: "error.main", weight: 760, error: true };
  if (level === "warning" || level === "warn") return { label: "警告", color: "warning.dark", weight: 600, error: false };
  if (level === "debug") return { label: "调试", color: "text.secondary", weight: 400, error: false };
  return { label: "信息", color: "text.primary", weight: 400, error: false };
}
