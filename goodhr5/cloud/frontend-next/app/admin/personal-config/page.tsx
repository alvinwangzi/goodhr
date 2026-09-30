/** 本文件负责新版后台个人操作节奏和模拟休息配置。 */
"use client";

import PsychologyAltRoundedIcon from "@mui/icons-material/PsychologyAltRounded";
import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import TimerOutlinedIcon from "@mui/icons-material/TimerOutlined";
import RepeatRoundedIcon from "@mui/icons-material/RepeatRounded";
import {
  Box,
  Button,
  InputAdornment,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
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
  re_greet_interval_min: 30,
  re_greet_interval_max: 50,
  re_greet_time_range: 7,
  re_greet_max_count: 1,
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

        <SectionPanel>
          <SectionTitle
            icon={<RepeatRoundedIcon />}
            title='复打招呼'
            description='对之前打过招呼但未回复的候选人，间隔一段时间后再发一次招呼。全局生效，所有岗位共用。'
          />
          <Stack spacing={2.25} sx={{ mt: 2.5 }}>
            <NumberRange
              label='复打间隔'
              help='两次复打之间在此范围内随机等待，模拟人工节奏。'
              unit='分钟'
              min={form.re_greet_interval_min}
              max={form.re_greet_interval_max}
              onMin={(value) => setNumber("re_greet_interval_min", value)}
              onMax={(value) => setNumber("re_greet_interval_max", value)}
            />
            <CompactNumber
              label='复打时间范围'
              help='只复打最近 N 天内打过招呼的候选人，更早的不再打扰。'
              unit='天'
              value={form.re_greet_time_range}
              onChange={(value) => setNumber("re_greet_time_range", value)}
            />
            <CompactNumber
              label='同一候选人最多复打次数'
              help='达到上限后该候选人不再进入复打名单。'
              unit='次'
              value={form.re_greet_max_count}
              onChange={(value) => setNumber("re_greet_max_count", value)}
            />
          </Stack>
        </SectionPanel>
      </Box>
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
