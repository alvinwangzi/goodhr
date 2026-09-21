/** 本文件负责新版后台 AI 配置页面，用于填写和测试自定义 AI 接口。 */
"use client";

import ApiRoundedIcon from "@mui/icons-material/ApiRounded";
import PsychologyAltRoundedIcon from "@mui/icons-material/PsychologyAltRounded";
import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import ScienceRoundedIcon from "@mui/icons-material/ScienceRounded";
import {
  Alert,
  Box,
  Button,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import Link from "next/link";
import { useEffect, useState } from "react";
import { cloudRequest } from "@/lib/admin-api";
import { PageHeader, SectionPanel } from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";

const defaults = {
  base_url: "https://maas.qianwenaiapi.com/compatible-mode/v1/chat/completions",
  model: "qwen3.7-plus",
  api_key: "",
};

/** normalizeAIBaseURL 补全 OpenAI 兼容的 Chat Completions 地址。 */
function normalizeAIBaseURL(baseURL: string) {
  const value = baseURL.trim().replace(/\/+$/, "");
  if (!value) return "";
  if (value.endsWith("/chat/completions")) return value;
  if (value.endsWith("/v1")) return `${value}/chat/completions`;
  return `${value}/v1/chat/completions`;
}

/** AIConfigPage 管理自定义 AI 接口配置。 */
export default function AIConfigPage() {
  const { notify } = useAdmin();
  const [form, setForm] = useState({ ...defaults });
  const [keySet, setKeySet] = useState(false);
  const [loading, setLoading] = useState(false);

  /** load 读取当前用户的 AI 配置。 */
  async function load() {
    setLoading(true);
    try {
      const data = await cloudRequest("/api/config/user-ai");
      const ai = data.config || {};
      setKeySet(Boolean(ai.api_key_set));
      // 如果是内置 AI 的代理地址，重置为千问默认地址
      const baseURL = ai.base_url || "";
      const isBuiltinProxy = baseURL.includes("goodhr5.58it.cn") || baseURL.includes("127.0.0.1");
      setForm({
        ...defaults,
        base_url: isBuiltinProxy ? defaults.base_url : (baseURL || defaults.base_url),
        model: ai.model || defaults.model,
        api_key: "", // API Key 始终不回填，保护敏感信息
      });
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
  }, []);

  /** testAI 通过云端代理验证当前填写的 AI 接口。 */
  async function testAI() {
    if (!form.api_key.trim()) return notify("测试前请填写 AI Key", "warning");
    const baseURL = normalizeAIBaseURL(form.base_url);
    if (!baseURL || !form.model.trim())
      return notify("请填写 AI 地址和模型", "warning");
    setLoading(true);
    try {
      setForm((current) => ({ ...current, base_url: baseURL }));
      await cloudRequest("/api/config/test-ai", {
        method: "POST",
        body: {
          base_url: baseURL,
          model: form.model.trim(),
          api_key: form.api_key.trim(),
          temperature: 0,
          enabled: true,
        },
      });
      notify("AI 接口测试成功", "success");
      // 测试成功后自动保存配置
      try {
        await cloudRequest("/api/config/user-ai", {
          method: "PUT",
          body: {
            base_url: baseURL,
            model: form.model.trim(),
            api_key: form.api_key.trim(),
            temperature: 0,
            prompt_template: "",
            enabled: true,
          },
        });
        setKeySet(true);
        notify("配置已自动保存", "success");
      } catch {
        notify("测试成功但保存失败，请手动点击保存", "warning");
      }
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "AI 接口测试失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  /** save 保存 AI 配置。 */
  async function save() {
    const baseURL = normalizeAIBaseURL(form.base_url);
    if (!baseURL || !form.model.trim())
      return notify("请填写 AI 地址和模型", "warning");
    if (!keySet && !form.api_key.trim())
      return notify("请填写 AI Key", "warning");
    setLoading(true);
    try {
      setForm((current) => ({ ...current, base_url: baseURL }));
      await cloudRequest("/api/config/user-ai", {
        method: "PUT",
        body: {
          base_url: baseURL,
          model: form.model.trim(),
          api_key: form.api_key.trim(),
          temperature: 0,
          prompt_template: "",
          enabled: true,
        },
      });
      setKeySet(true);
      notify("AI 配置已保存", "success");
    } catch (error) {
      notify(error instanceof Error ? error.message : "保存配置失败", "error");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <PageHeader
        title="AI 配置"
        description="填写自己的 AI 接口地址、模型和 Key，保存后用于岗位运行中的 AI 筛选和识别。"
        actions={
          <Button
            variant="contained"
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
          description='前往千问平台申请多模态模型和 API Key，然后填写到下方表单中。'
        />
      </Box>

      <SectionPanel
        sx={{
          mb: 2,
          borderColor: "primary.light",
          bgcolor: "action.selected",
          boxShadow: "0 16px 44px rgba(17, 17, 17, .08)",
        }}
      >
        <SectionTitle
          icon={<ApiRoundedIcon />}
          title="自定义 AI"
          description="填写自己的 AI 接口，建议先测试成功，再点击页面右上角保存。"
        />
        <Alert
          severity="info"
          icon={<ApiRoundedIcon />}
          sx={{
            mt: 2,
            mb: 2,
            border: "1px solid",
            borderColor: "primary.light",
            bgcolor: "action.hover",
            color: "text.primary",
            "& .MuiAlert-icon": { color: "primary.main" },
          }}
        >
          可接入兼容 OpenAI 格式的多模态模型，例如千问、硅基流动和
          OpenAI。模型必须支持图片识别；DeepSeek
          当前不支持图片输入，请不要用于详情 AI 识别。
        </Alert>
        <Box
          sx={{
            display: "grid",
            gridTemplateColumns: {
              xs: "1fr",
              lg: "minmax(0, 1.55fr) minmax(220px, .65fr)",
            },
            gap: 2,
          }}
        >
          <TextField
            label="API 地址"
            value={form.base_url}
            onChange={(event) =>
              setForm({ ...form, base_url: event.target.value })
            }
            helperText="默认使用千问兼容 OpenAI 的 Chat Completions 地址。"
          />
          <TextField
            label="模型名称"
            value={form.model}
            onChange={(event) =>
              setForm({ ...form, model: event.target.value })
            }
            helperText="例如 qwen3.7-plus"
          />
          <TextField
            label="API Key"
            value={form.api_key}
            onChange={(event) =>
              setForm({ ...form, api_key: event.target.value })
            }
            placeholder="sk-sp-xxxxx"
            helperText="千问 Token Plan 用户的 Key 通常以 sk-sp- 开头，请在千问 API Key 页面获取。"
            sx={{ gridColumn: { lg: "1 / -1" }, maxWidth: 760 }}
          />
        </Box>
        <Stack
          direction={{ xs: "column", sm: "row" }}
          spacing={1.25}
          sx={{ mt: 2.25, alignItems: { sm: "center" } }}
        >
          <Button
            variant="contained"
            startIcon={<ScienceRoundedIcon />}
            disabled={loading}
            onClick={() => void testAI()}
            sx={{ borderRadius: "999px", px: 2.4 }}
          >
            测试一下
          </Button>

          <Typography sx={{ color: "text.secondary", fontSize: 13 }}>
            {keySet ? "当前已有保存过的 AI Key。" : "当前还没有保存 AI Key。"}
            测试成功后，请点击页面右上角保存配置。
          </Typography>
        </Stack>
      </SectionPanel>
    </>
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
    <Stack direction="row" spacing={1.25} sx={{ alignItems: "center" }}>
      <Box sx={{ color: "primary.main", display: "grid", placeItems: "center" }}>
        {icon}
      </Box>
      <Box>
        <Typography component="h2" sx={{ fontSize: 19, fontWeight: 760 }}>
          {title}
        </Typography>
        <Typography sx={{ mt: 0.25, color: "text.secondary", fontSize: 13 }}>
          {description}
        </Typography>
      </Box>
    </Stack>
  );
}

/** QuickLink 展示外部帮助入口卡片。 */
function QuickLink({
  href,
  external,
  icon,
  eyebrow,
  title,
  description,
  primary,
}: {
  href: string;
  external?: boolean;
  icon: React.ReactNode;
  eyebrow: string;
  title: string;
  description: string;
  primary?: boolean;
}) {
  const content = (
    <Stack direction="row" spacing={2} sx={{ alignItems: "center" }}>
      <Box sx={{ color: primary ? "primary.main" : "text.secondary" }}>
        {icon}
      </Box>
      <Box sx={{ flex: 1 }}>
        <Typography sx={{ fontSize: 12, color: "text.secondary", fontWeight: 700 }}>
          {eyebrow}
        </Typography>
        <Typography sx={{ fontSize: 16, fontWeight: 700 }}>{title}</Typography>
        <Typography sx={{ fontSize: 13, color: "text.secondary" }}>
          {description}
        </Typography>
      </Box>
      <Typography sx={{ color: "text.secondary" }}>→</Typography>
    </Stack>
  );
  const sx = {
    display: "block",
    p: 2.5,
    border: "1px solid",
    borderColor: primary ? "primary.main" : "divider",
    borderRadius: 2,
    bgcolor: primary ? "action.hover" : "background.paper",
    color: "text.primary",
    textDecoration: "none",
    transition: "all 150ms ease",
    "&:hover": { borderColor: "primary.main", bgcolor: "action.hover" },
  };
  if (external) {
    return (
      <Box component="a" href={href} target="_blank" rel="noopener noreferrer" sx={sx}>
        {content}
      </Box>
    );
  }
  return (
    <Link href={href} style={{ textDecoration: "none" }}>
      <Box sx={sx}>{content}</Box>
    </Link>
  );
}
