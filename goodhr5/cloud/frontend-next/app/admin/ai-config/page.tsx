/** 本文件负责管理端「AI配置」页面，填写并测试自定义 OpenAI 兼容 AI 接入。 */
"use client";

import ArrowOutwardRoundedIcon from "@mui/icons-material/ArrowOutwardRounded";
import PsychologyAltRoundedIcon from "@mui/icons-material/PsychologyAltRounded";
import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import ScienceOutlinedIcon from "@mui/icons-material/ScienceOutlined";
import Visibility from "@mui/icons-material/Visibility";
import VisibilityOff from "@mui/icons-material/VisibilityOff";
import {
  Box,
  Button,
  IconButton,
  InputAdornment,
  Popover,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import Link from "next/link";
import { useEffect, useState } from "react";
import { cloudRequest } from "@/lib/admin-api";
import { PageHeader, SectionPanel } from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";

// 千问平台 OpenAI 兼容接入地址，作为表单默认与旧代理地址的替换值。
const QIANWEN_BASE_URL = "https://maas.qianwenaiapi.com/compatible-mode/v1";

type AIFormState = {
  base_url: string;
  model: string;
  api_key: string;
  temperature: number;
  prompt_template: string;
  enabled: boolean;
};

// 表单默认值：接口地址默认千问，配置完整即启用。
const defaults: AIFormState = {
  base_url: QIANWEN_BASE_URL,
  model: "",
  api_key: "",
  temperature: 0,
  prompt_template: "",
  enabled: true,
};

/** AIConfigPage 管理自定义 AI 接入配置，测试通过后自动保存。 */
export default function AIConfigPage() {
  const { notify } = useAdmin();
  const [form, setForm] = useState<AIFormState>({ ...defaults });
  const [loading, setLoading] = useState(false);
  const [testing, setTesting] = useState(false);
  const [showKey, setShowKey] = useState(false);
  const [testError, setTestError] = useState("");
  const [helpAnchor, setHelpAnchor] = useState<HTMLElement | null>(null);

  /** load 读取当前用户的自定义 AI 配置并回填表单。 */
  async function load() {
    setLoading(true);
    try {
      const data = await cloudRequest("/api/config/user-ai");
      const config = data.config;
      if (!config) {
        return;
      }
      setForm((current) => ({
        ...current,
        base_url: normalizeLoadedBaseURL(config.base_url),
        model: config.model || "",
        api_key: config.api_key || "",
        temperature: Number(config.temperature ?? 0),
        prompt_template: config.prompt_template || "",
        enabled: config.enabled !== false,
      }));
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "AI 配置读取失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** save 保存自定义 AI 配置，供测试成功自动调用和手动兜底。 */
  async function save(silent = false) {
    setLoading(true);
    try {
      await cloudRequest("/api/config/user-ai", {
        method: "PUT",
        body: { ...form },
      });
      if (!silent) {
        notify("配置已保存", "success");
      }
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "保存配置失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  /** testAI 先调用云端测试 AI 接口，成功后自动保存配置。 */
  async function testAI() {
    if (!form.api_key.trim()) {
      notify("请填写 API Key", "warning");
      return;
    }
    if (!form.model.trim()) {
      notify("请填写模型名称", "warning");
      return;
    }
    setTestError("");
    setTesting(true);
    try {
      await cloudRequest("/api/config/test-ai", {
        method: "POST",
        body: { ...form },
      });
      try {
        await cloudRequest("/api/config/user-ai", {
          method: "PUT",
          body: { ...form },
        });
        notify("AI 连接成功，配置已自动保存", "success");
      } catch {
        notify("AI 连接成功，但自动保存失败，请手动保存", "warning");
      }
    } catch (error) {
      setTestError(parseTestError(error));
    } finally {
      setTesting(false);
    }
  }

  /** setText 更新一个文本配置字段。 */
  function setText(key: "base_url" | "model" | "api_key", value: string) {
    setForm((current) => ({ ...current, [key]: value }));
    setTestError("");
  }

  return (
    <>
      <PageHeader
        title='AI配置'
        description='填写 OpenAI 兼容接口地址、模型和 Key，测试通过后自动保存，用于岗位 AI 筛选。'
        actions={
          <Button
            variant='contained'
            startIcon={<SaveRoundedIcon />}
            disabled={loading}
            onClick={() => void save()}
          >
            {loading ? "处理中" : "保存配置"}
          </Button>
        }
      />

      <Box sx={{ mb: 2 }}>
        <QuickLink
          href='https://www.qianwenai.com/'
          external
          icon={<PsychologyAltRoundedIcon />}
          eyebrow='AI 接入'
          title='获取 AI 接口'
          description='前往千问平台申请多模态模型和 API Key，然后在本页填写。'
        />
      </Box>

      <SectionPanel>
        <SectionTitle
          icon={<ScienceOutlinedIcon />}
          title='自定义 AI'
          description='请配置多模态大模型，即支持文字、图片输入的大模型，比如 qwen3.7-plus、qwen3.8-max 等。'
        />
        <Stack spacing={2.25} sx={{ mt: 2.5, maxWidth: 800 }}>
          <LabeledField
            label='API 地址'
            help='OpenAI 兼容的服务地址，通常以 /v1 结尾。'
          >
            <TextField
              size='small'
              value={form.base_url}
              placeholder={QIANWEN_BASE_URL}
              onChange={(event) => setText("base_url", event.target.value)}
            />
          </LabeledField>
          <LabeledField label='模型名称' help={
            <Typography component="span" onClick={(e) => setHelpAnchor(e.currentTarget)} sx={{ color: "primary.main", fontSize: 12, cursor: "pointer", "&:hover": { textDecoration: "underline" } }}>如何判断多模态模型</Typography>
          }>
            <TextField
              size='small'
              value={form.model}
              placeholder='例如 qwen3-vl-plus'
              onChange={(event) => setText("model", event.target.value)}
            />
          </LabeledField>
          <LabeledField label='API Key' help='平台颁发的访问密钥，仅保存在你的账号下。'>
            <TextField
              size='small'
              type={showKey ? "text" : "password"}
              value={form.api_key}
              onChange={(event) => setText("api_key", event.target.value)}
              slotProps={{
                input: {
                  endAdornment: (
                    <InputAdornment position='end'>
                      <IconButton
                        aria-label={showKey ? "隐藏 API Key" : "显示 API Key"}
                        onClick={() => setShowKey((value) => !value)}
                        edge='end'
                        size='small'
                      >
                        {showKey ? <VisibilityOff /> : <Visibility />}
                      </IconButton>
                    </InputAdornment>
                  ),
                },
              }}
            />
          </LabeledField>
          <Stack spacing={1} sx={{ alignItems: "flex-start" }}>
            <Stack direction='row' spacing={1.5} sx={{ alignItems: "center" }}>
              <Button
                variant='contained'
                startIcon={<ScienceOutlinedIcon />}
                disabled={testing || loading}
                onClick={() => void testAI()}
              >
                {testing ? "测试中" : "测试AI是否配置正确"}
              </Button>
              <Typography sx={{ color: "text.secondary", fontSize: 12.5 }}>
                测试通过后会自动保存配置，无需再点保存。
              </Typography>
            </Stack>
            {testError ? (
              <Typography sx={{ color: "error.main", fontSize: 13, lineHeight: 1.6 }}>
                {testError}
              </Typography>
            ) : null}
          </Stack>
        </Stack>
      </SectionPanel>
      <Popover
        open={Boolean(helpAnchor)}
        anchorEl={helpAnchor}
        onClose={() => setHelpAnchor(null)}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        sx={{ "& .MuiPaper-root": { maxWidth: 420, p: 2 } }}
      >
        <Typography sx={{ fontSize: 13.5, lineHeight: 1.8, color: "text.primary" }}>
          在千问模型页面找到你想用的模型，看模型卡片上的<b>能力图标</b>区域：
        </Typography>
        <Box component="img" src="/multimodal-guide.png" alt="多模态模型能力图标示例" sx={{ width: "100%", mt: 1.5, mb: 1, borderRadius: 1, border: "1px solid", borderColor: "divider" }} />
        <Typography sx={{ fontSize: 13.5, lineHeight: 1.8, color: "text.primary" }}>
          如上图，显示 <b>图片 + 文字 → 文字</b> 的图标，说明支持图片和文字输入，就是多模态大模型。
        </Typography>
        <Typography sx={{ mt: 0.5, fontSize: 13.5, lineHeight: 1.8, color: "text.primary" }}>
          如果只有文字图标，则不支持图片输入，不能用在本系统中。
        </Typography>
        <Link href="https://www.qianwenai.com/models" target="_blank" rel="noreferrer" style={{ display: "inline-block", marginTop: 12, color: "primary.main", fontSize: 13, fontWeight: 600, textDecoration: "none" }}>
          去千问模型页面查看 →
        </Link>
      </Popover>
    </>
  );
}

// normalizeLoadedBaseURL 处理历史配置里的内置代理地址，统一替换为千问默认地址。
function normalizeLoadedBaseURL(value: string) {
  const trimmed = (value || "").trim();
  if (!trimmed || trimmed.includes("58it.cn")) {
    return QIANWEN_BASE_URL;
  }
  return trimmed;
}

// parseTestError 将 AI 测试失败的原始错误转为友好的中文提示。
function parseTestError(error: unknown): string {
  const message = error instanceof Error ? error.message : "";
  // API Key 无效
  if (message.includes("401") || message.toLowerCase().includes("invalid_api_key") || message.toLowerCase().includes("authentication")) {
    return "API Key 无效，请检查是否填写正确，或到千问平台重新获取。";
  }
  // 模型不存在
  if (message.includes("404") || message.toLowerCase().includes("model_not_found")) {
    return "找不到这个模型，请确认模型名称填写正确。";
  }
  // 接口地址错误
  if (message.includes("ENOTFOUND") || message.includes("ECONNREFUSED") || message.includes("getaddrinfo")) {
    return "无法连接到 API 地址，请检查地址是否正确、网络是否通畅。";
  }
  // 请求超时
  if (message.includes("timeout") || message.includes("ETIMEDOUT") || message.includes("AbortError")) {
    return "请求超时，AI 服务响应太慢，请稍后重试。";
  }
  // 配额/限流
  if (message.includes("429") || message.toLowerCase().includes("rate_limit") || message.toLowerCase().includes("quota")) {
    return "调用次数已用完或被限流，请检查账户余额或稍后重试。";
  }
  // 兜底
  return message || "AI 测试失败，请检查配置后重试。";
}

/** LabeledField 展示一行带说明的表单项。 */
function LabeledField({
  label,
  help,
  children,
}: {
  label: string;
  help: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: { xs: "1fr", sm: "minmax(100px, 140px) 1fr" },
        gap: 1.5,
        alignItems: "center",
      }}
    >
      <Box>
        <Typography sx={{ fontWeight: 700 }}>{label}</Typography>
        <Typography sx={{ color: "text.secondary", fontSize: 12 }}>
          {help}
        </Typography>
      </Box>
      {children}
    </Box>
  );
}

/** QuickLink 展示 AI 配置页的外部帮助入口。 */
function QuickLink({
  href,
  icon,
  eyebrow,
  title,
  description,
  external = false,
}: {
  href: string;
  icon: React.ReactNode;
  eyebrow: string;
  title: string;
  description: string;
  external?: boolean;
}) {
  const content = (
    <Stack
      direction='row'
      spacing={1.75}
      sx={{
        p: 2,
        minHeight: 118,
        height: "100%",
        alignItems: "center",
        border: "1px solid",
        borderColor: "divider",
        borderRadius: "8px",
        bgcolor: "action.hover",
        color: "text.primary",
        transition: "150ms ease",
        "&:hover": {
          borderColor: "primary.main",
          bgcolor: "action.hover",
          transform: "translateY(-1px)",
        },
      }}
    >
      <Box
        sx={{
          width: 46,
          height: 46,
          borderRadius: "999px",
          display: "grid",
          placeItems: "center",
          bgcolor: "action.selected",
          color: "primary.main",
          flexShrink: 0,
          "& .MuiSvgIcon-root": { fontSize: 24 },
        }}
      >
        {icon}
      </Box>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Typography
          sx={{
            mb: 0.45,
            width: "fit-content",
            px: 1,
            py: 0.35,
            borderRadius: "999px",
            bgcolor: "action.selected",
            color: "primary.dark",
            fontSize: 12,
            fontWeight: 760,
          }}
        >
          {eyebrow}
        </Typography>
        <Typography sx={{ fontSize: 17, fontWeight: 820, lineHeight: 1.2 }}>
          {title}
        </Typography>
        <Typography
          sx={{
            mt: 0.75,
            color: "text.secondary",
            fontSize: 13.5,
            lineHeight: 1.65,
          }}
        >
          {description}
        </Typography>
      </Box>
      <ArrowOutwardRoundedIcon
        sx={{ color: "text.secondary", fontSize: 22, flexShrink: 0 }}
      />
    </Stack>
  );
  return external ? (
    <Box
      component='a'
      href={href}
      target='_blank'
      rel='noreferrer'
      sx={{ textDecoration: "none" }}
    >
      {content}
    </Box>
  ) : (
    <Link href={href} style={{ textDecoration: "none" }}>
      {content}
    </Link>
  );
}

/** SectionTitle 展示配置区域标题和说明。 */
function SectionTitle({
  icon,
  title,
  description,
}: {
  icon: React.ReactNode;
  title: string;
  description: string;
}) {
  return (
    <Stack direction='row' spacing={1.25} sx={{ alignItems: "center" }}>
      <Box sx={{ color: "primary.main", display: "grid", placeItems: "center" }}>
        {icon}
      </Box>
      <Box>
        <Typography component='h2' sx={{ fontSize: 19, fontWeight: 760 }}>
          {title}
        </Typography>
        <Typography sx={{ mt: 0.25, color: "text.secondary", fontSize: 13 }}>
          {description}
        </Typography>
      </Box>
    </Stack>
  );
}
