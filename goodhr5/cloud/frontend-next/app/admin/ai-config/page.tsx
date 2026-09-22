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
      notify(
        error instanceof Error ? error.message : "AI 测试失败",
        "error",
      );
    } finally {
      setTesting(false);
    }
  }

  /** setText 更新一个文本配置字段。 */
  function setText(key: "base_url" | "model" | "api_key", value: string) {
    setForm((current) => ({ ...current, [key]: value }));
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
          description='岗位 AI 筛选优先使用这里保存的接口配置。'
        />
        <Stack spacing={2.25} sx={{ mt: 2.5, maxWidth: 640 }}>
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
          <LabeledField label='模型名称' help='与接口服务匹配的模型 ID。'>
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
          <Stack direction='row' spacing={1.5} sx={{ alignItems: "center" }}>
            <Button
              variant='contained'
              startIcon={<ScienceOutlinedIcon />}
              disabled={testing || loading}
              onClick={() => void testAI()}
            >
              {testing ? "测试中" : "先测试 AI"}
            </Button>
            <Typography sx={{ color: "text.secondary", fontSize: 12.5 }}>
              测试通过后会自动保存配置，无需再点保存。
            </Typography>
          </Stack>
        </Stack>
      </SectionPanel>
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

/** LabeledField 展示一行带说明的表单项。 */
function LabeledField({
  label,
  help,
  children,
}: {
  label: string;
  help: string;
  children: React.ReactNode;
}) {
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: { xs: "1fr", sm: "minmax(160px, 1fr) 260px" },
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
