/** 本文件负责新版后台个人操作节奏和模拟休息配置。 */
"use client";

import ArrowOutwardRoundedIcon from "@mui/icons-material/ArrowOutwardRounded";
import PsychologyAltRoundedIcon from "@mui/icons-material/PsychologyAltRounded";
import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import TimerOutlinedIcon from "@mui/icons-material/TimerOutlined";
import {
  Box,
  Button,
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
import NotificationProfileDialog from "@/components/admin/NotificationProfileDialog";

const defaults = {
  click_frequency: 80,
  detail_open_probability: 80,
  detail_open_delay_min: 1,
  detail_open_delay_max: 2,
  detail_close_delay_min: 0,
  detail_close_delay_max: 0,
  greet_before_delay_min: 1,
  greet_before_delay_max: 2,
  rest_after_candidates_min: 40,
  rest_after_candidates_max: 70,
  rest_times_min: 2,
  rest_times_max: 3,
  rest_duration_min: 2,
  rest_duration_max: 7,
};

/** PersonalConfigPage 管理操作节奏和模拟人工操作参数。 */
export default function PersonalConfigPage() {
  const { notify } = useAdmin();
  const [form, setForm] = useState({ ...defaults });
  const [loading, setLoading] = useState(false);
  const [profileOpenSignal, setProfileOpenSignal] = useState(0);

  /** load 读取个人操作偏好。 */
  async function load() {
    setLoading(true);
    try {
      const data = await cloudRequest("/api/config/user-preferences");
      const preference = data.config || {};
      setForm({ ...defaults, ...preference });
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "个人配置读取失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  /** save 保存操作偏好。 */
  async function save() {
    setLoading(true);
    try {
      await cloudRequest("/api/config/user-preferences", {
        method: "PUT",
        body: { ...form },
      });
      notify("个人配置已保存", "success");
    } catch (error) {
      notify(error instanceof Error ? error.message : "保存配置失败", "error");
    } finally {
      setLoading(false);
    }
  }

  /** setNumber 更新一个数字配置字段。 */
  function setNumber(key: keyof typeof defaults, value: string) {
    setForm((current) => ({ ...current, [key]: Number(value || 0) }));
  }

  return (
    <>
      <NotificationProfileDialog openSignal={profileOpenSignal} />
      <PageHeader
        title='个人配置'
        description='设置岗位运行的操作节奏和模拟人工参数，保存后会用于本地岗位运行。'
        actions={
          <>
            <Button
              variant='contained'
              startIcon={<SaveRoundedIcon />}
              disabled={loading}
              onClick={() => void save()}
            >
              {loading ? "处理中" : "保存配置"}
            </Button>
          </>
        }
      />

      <Box sx={{ mb: 2 }}>
        <QuickLink
          href='https://www.qianwenai.com/'
          external
          icon={<PsychologyAltRoundedIcon />}
          eyebrow='AI 接入'
          title='获取 AI 接口'
          description='前往千问平台申请多模态模型和 API Key，然后在「AI配置」中填写。'
        />
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", xl: "repeat(2, minmax(0, 1fr))" },
          gap: 2,
        }}
      >
        <SectionPanel>
          <SectionTitle
            icon={<TimerOutlinedIcon />}
            title='操作节奏'
            description='在范围内随机等待，让岗位运行操作保持自然。'
          />
          <Stack spacing={2.25} sx={{ mt: 2.5 }}>
            <CompactNumber
              label='点击频率'
              help='控制候选人操作的执行比例。'
              unit='%'
              value={form.click_frequency}
              onChange={(value) => setNumber("click_frequency", value)}
            />
            <CompactNumber
              label='详情查看概率'
              help='关键词模式下，决定是否打开详情继续筛选。'
              unit='%'
              value={form.detail_open_probability}
              onChange={(value) => setNumber("detail_open_probability", value)}
            />
            <NumberRange
              label='打开详情前延时'
              help='点击候选人详情前随机等待。'
              unit='秒'
              min={form.detail_open_delay_min}
              max={form.detail_open_delay_max}
              onMin={(value) => setNumber("detail_open_delay_min", value)}
              onMax={(value) => setNumber("detail_open_delay_max", value)}
            />
            <NumberRange
              label='关闭详情前延时'
              help='详情提取完成后、关闭页面前随机等待。'
              unit='秒'
              min={form.detail_close_delay_min}
              max={form.detail_close_delay_max}
              onMin={(value) => setNumber("detail_close_delay_min", value)}
              onMax={(value) => setNumber("detail_close_delay_max", value)}
            />
            <NumberRange
              label='打招呼前延时'
              help='候选人通过筛选后、打招呼前随机等待。'
              unit='秒'
              min={form.greet_before_delay_min}
              max={form.greet_before_delay_max}
              onMin={(value) => setNumber("greet_before_delay_min", value)}
              onMax={(value) => setNumber("greet_before_delay_max", value)}
            />
          </Stack>
        </SectionPanel>

        <SectionPanel>
          <SectionTitle
            icon={<PsychologyAltRoundedIcon />}
            title='模拟休息'
            description='岗位运行会按配置间歇休息，避免长时间连续操作。'
          />
          <Stack spacing={2.25} sx={{ mt: 2.5 }}>
            <NumberRange
              label='处理多少人后休息'
              help='例如设置 40 到 70，系统会随机选择人数。'
              unit='人'
              min={form.rest_after_candidates_min}
              max={form.rest_after_candidates_max}
              onMin={(value) => setNumber("rest_after_candidates_min", value)}
              onMax={(value) => setNumber("rest_after_candidates_max", value)}
            />
            <NumberRange
              label='单次岗位运行休息次数'
              help='达到本次随机次数后不再休息。'
              unit='次'
              min={form.rest_times_min}
              max={form.rest_times_max}
              onMin={(value) => setNumber("rest_times_min", value)}
              onMax={(value) => setNumber("rest_times_max", value)}
            />
            <NumberRange
              label='每次休息时长'
              help='每次休息会在此范围内随机，并写入岗位运行日志。'
              unit='分钟'
              min={form.rest_duration_min}
              max={form.rest_duration_max}
              onMin={(value) => setNumber("rest_duration_min", value)}
              onMax={(value) => setNumber("rest_duration_max", value)}
            />
          </Stack>
        </SectionPanel>
      </Box>
    </>
  );
}

/** QuickLink 展示个人配置页的外部帮助入口。 */
function QuickLink({
  href,
  icon,
  eyebrow,
  title,
  description,
  external = false,
  primary = false,
}: {
  href: string;
  icon: React.ReactNode;
  eyebrow: string;
  title: string;
  description: string;
  external?: boolean;
  primary?: boolean;
}) {
  const content = (
    <Stack
      direction='row'
      spacing={1.75}
      sx={{
        p: { xs: 2, md: primary ? 2.5 : 2 },
        minHeight: primary ? 140 : 118,
        height: "100%",
        alignItems: "center",
        border: "1px solid",
        borderColor: primary ? "primary.main" : "divider",
        borderRadius: "8px",
        bgcolor: primary ? "action.selected" : "action.hover",
        color: "text.primary",
        boxShadow: primary ? "0 18px 44px rgba(17, 17, 17, .1)" : "none",
        transition: "150ms ease",
        "&:hover": {
          borderColor: "primary.main",
          bgcolor: primary ? "primary.light" : "action.hover",
          transform: "translateY(-1px)",
        },
      }}
    >
      <Box
        sx={{
          width: primary ? 58 : 46,
          height: primary ? 58 : 46,
          borderRadius: "999px",
          display: "grid",
          placeItems: "center",
          bgcolor: primary ? "primary.main" : "action.selected",
          color: primary ? "primary.contrastText" : "primary.main",
          flexShrink: 0,
          "& .MuiSvgIcon-root": { fontSize: primary ? 31 : 24 },
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
        <Typography
          sx={{ fontSize: primary ? 22 : 17, fontWeight: 820, lineHeight: 1.2 }}
        >
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
        sx={{
          color: primary ? "primary.main" : "text.secondary",
          fontSize: 22,
          flexShrink: 0,
        }}
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

/** CompactNumber 展示一个带单位的紧凑数字配置。 */
function CompactNumber({
  label,
  help,
  unit,
  value,
  onChange,
}: {
  label: string;
  help: string;
  unit: string;
  value: number;
  onChange: (value: string) => void;
}) {
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: { xs: "1fr", sm: "minmax(160px, 1fr) 150px" },
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
      <TextField
        size='small'
        type='number'
        value={value}
        onChange={(event) => onChange(event.target.value)}
        slotProps={{
          input: {
            endAdornment: (
              <InputAdornment position='end'>{unit}</InputAdornment>
            ),
          },
        }}
      />
    </Box>
  );
}

/** NumberRange 展示一组带说明的最小值和最大值输入。 */
function NumberRange({
  label,
  help,
  unit,
  min,
  max,
  onMin,
  onMax,
}: {
  label: string;
  help: string;
  unit: string;
  min: number;
  max: number;
  onMin: (value: string) => void;
  onMax: (value: string) => void;
}) {
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: { xs: "1fr", sm: "minmax(160px, 1fr) 250px" },
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
      <Stack direction='row' spacing={1} sx={{ alignItems: "center" }}>
        <TextField
          size='small'
          aria-label={`${label}最小值`}
          type='number'
          value={min}
          onChange={(event) => onMin(event.target.value)}
          sx={{ minWidth: 0 }}
        />
        <Typography color='text.secondary'>至</Typography>
        <TextField
          size='small'
          aria-label={`${label}最大值`}
          type='number'
          value={max}
          onChange={(event) => onMax(event.target.value)}
          sx={{ minWidth: 0 }}
          slotProps={{
            input: {
              endAdornment: (
                <InputAdornment position='end'>{unit}</InputAdornment>
              ),
            },
          }}
        />
      </Stack>
    </Box>
  );
}
