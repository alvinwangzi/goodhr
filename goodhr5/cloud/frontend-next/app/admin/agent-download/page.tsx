/** 本文件负责展示本地运行组件和组件更新入口。 */
"use client";

import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import {
  Box,
  Button,
  Chip,
  LinearProgress,
  Stack,
  Typography,
} from "@mui/material";
import { useEffect, useState } from "react";
import { cloudRequest, localRequest } from "@/lib/admin-api";
import {
  buildRuntimeInstallPayload,
  missingRequiredWinRuntimeURLs,
} from "@/lib/admin-runtime";
import {
  EmptyState,
  PageHeader,
  RefreshButton,
  SectionPanel,
} from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";

type UnknownRecord = Record<string, unknown>;

type RuntimeComponentView = {
  key: string;
  name: string;
  required: boolean;
  bundled: boolean;
  installed: boolean;
  configVersion: string;
  installedVersion: string;
  url: string;
  note: string;
  path: string;
};

const componentNames: Record<string, string> = {
  node_runtime: "Node 运行环境",
  node_worker: "浏览器控制 Worker",
  cloakbrowser: "CloakBrowser 浏览器",
  ocr: "OCR 组件",
};

// componentStatusKeys 把组件键映射到本地程序状态接口的真实安装标志字段，
// 避免用“有路径”代替“已安装”造成误报。
const componentStatusKeys: Record<string, string> = {
  node_runtime: "node_installed",
  node_worker: "worker_installed",
  cloakbrowser: "cloakbrowser_installed",
  ocr: "ocr_installed",
};

/** AgentDownloadPage 展示组件状态并触发运行组件更新。 */
export default function AgentDownloadPage() {
  const { agentBase, onboardingConfig, refreshAgent, notify } = useAdmin();
  const [runtime, setRuntime] = useState<UnknownRecord>({});
  const [loading, setLoading] = useState(false);

  /** load 读取本地运行状态，并从健康接口补齐程序版本和数据目录。 */
  async function load() {
    if (!agentBase) return;
    setLoading(true);
    try {
      const [status, health] = await Promise.all([
        localRequest(agentBase, "/api/v1/runtime/status"),
        localRequest(agentBase, "/health").catch(() => null),
      ]);
      const statusRecord = asRecord(status);
      const healthRecord = asRecord(health);
      setRuntime({
        ...statusRecord,
        // 旧版本地程序的 runtime/status 不带版本和数据目录，回退用 /health 的值。
        version:
          textValue(statusRecord.version) || textValue(healthRecord.version),
        data_dir:
          textValue(statusRecord.data_dir) || textValue(healthRecord.dataDir),
      });
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "组件信息读取失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, [agentBase]);

  /** updateRuntime 下载并安装缺失或版本不符的运行组件。 */
  async function updateRuntime() {
    if (!agentBase) {
      notify("本地程序未连接", "error");
      return;
    }
    setLoading(true);
    try {
      let config: unknown = onboardingConfig;
      let missing = missingRequiredWinRuntimeURLs(config);
      if (missing.length) {
        const fresh = asRecord(await cloudRequest("/api/runtime/config"));
        config = fresh.config || {};
        missing = missingRequiredWinRuntimeURLs(config);
      }
      if (missing.length) {
        throw new Error(
          `运行组件下载地址没拿到：${missing.join("、")}。我重新拉了一次还是空，请检查系统配置。`,
        );
      }
      await localRequest(agentBase, "/api/v1/runtime/install", {
        method: "POST",
        body: buildRuntimeInstallPayload(config),
      });
      notify("组件更新岗位运行已完成", "success");
      await load();
    } catch (error) {
      notify(error instanceof Error ? error.message : "组件更新失败", "error");
    } finally {
      setLoading(false);
    }
  }

  const components = buildComponents(runtime, onboardingConfig);

  return (
    <>
      <PageHeader
        title="组件信息"
        description="查看本机运行组件、安装状态和版本。"
        actions={
          <>
            <RefreshButton
              loading={loading}
              onClick={() => void refreshAgent().then(load)}
            />
            <Button
              variant="contained"
              startIcon={<SystemUpdateAltRoundedIcon />}
              disabled={loading || !agentBase}
              onClick={() => void updateRuntime()}
            >
              更新运行组件
            </Button>
          </>
        }
      />
      {loading ? <LinearProgress sx={{ mb: 2 }} /> : null}
      {!agentBase ? (
        <SectionPanel>
          <EmptyState text="本地程序未连接" />
        </SectionPanel>
      ) : (
        <>
          <Box
            sx={{
              display: "grid",
              gridTemplateColumns: {
                xs: "1fr 1fr",
                md: "repeat(4, 1fr)",
              },
              gap: 2,
              mb: 2,
            }}
          >
            <SectionPanel>
              <Typography color="text.secondary" sx={{ fontSize: 12 }}>
                本地连接
              </Typography>
              <Typography
                sx={{ mt: 1, color: "primary.main", fontWeight: 760 }}
              >
                已连接
              </Typography>
            </SectionPanel>
            <SectionPanel>
              <Typography color="text.secondary" sx={{ fontSize: 12 }}>
                监听地址
              </Typography>
              <Typography sx={{ mt: 1, fontWeight: 760 }}>
                {agentBase}
              </Typography>
            </SectionPanel>
            <SectionPanel>
              <Typography color="text.secondary" sx={{ fontSize: 12 }}>
                程序版本
              </Typography>
              <Typography sx={{ mt: 1, fontWeight: 760 }}>
                {textValue(runtime.version) ||
                  textValue(runtime.agent_version) ||
                  "--"}
              </Typography>
            </SectionPanel>
            <SectionPanel>
              <Typography color="text.secondary" sx={{ fontSize: 12 }}>
                数据目录
              </Typography>
              <Typography
                sx={{ mt: 1, fontSize: 12, wordBreak: "break-all" }}
              >
                {textValue(runtime.data_dir) || "--"}
              </Typography>
            </SectionPanel>
          </Box>

          <Box
            sx={{
              display: "grid",
              gridTemplateColumns: { xs: "1fr", lg: "repeat(2, 1fr)" },
              gap: 2,
            }}
          >
            {components.map((item) => (
              <SectionPanel key={item.key}>
                <Stack
                  direction="row"
                  sx={{
                    justifyContent: "space-between",
                    alignItems: "flex-start",
                  }}
                >
                  <Box>
                    <Typography
                      component="h2"
                      sx={{ fontSize: 18, fontWeight: 760 }}
                    >
                      {item.name}
                    </Typography>
                    <Typography
                      sx={{ mt: 0.75, color: "text.secondary", fontSize: 13 }}
                    >
                      {item.note || "暂无版本说明"}
                    </Typography>
                  </Box>
                  <Chip
                    size="small"
                    color={
                      item.installed
                        ? "success"
                        : item.required
                          ? "error"
                          : "default"
                    }
                    label={
                      item.installed
                        ? "已安装"
                        : item.required
                          ? "未安装"
                          : "可选"
                    }
                  />
                </Stack>
                <Box
                  component="dl"
                  sx={{
                    mt: 2,
                    display: "grid",
                    gridTemplateColumns: "86px 1fr",
                    gap: 1,
                    fontSize: 13,
                    "& dt": { color: "text.secondary" },
                    "& dd": { m: 0, wordBreak: "break-all" },
                  }}
                >
                  <dt>配置版本</dt>
                  <dd>{item.configVersion || "--"}</dd>
                  <dt>本地版本</dt>
                  <dd>{item.installedVersion || "--"}</dd>
                  <dt>下载地址</dt>
                  <dd>
                    {item.bundled ? "随本地程序内置" : item.url || "未配置"}
                  </dd>
                  <dt>本地路径</dt>
                  <dd>{item.path || "--"}</dd>
                </Box>
              </SectionPanel>
            ))}
          </Box>
        </>
      )}
    </>
  );
}

/** buildComponents 根据本机系统构建组件展示数据。 */
function buildComponents(
  runtime: UnknownRecord,
  config: unknown,
): RuntimeComponentView[] {
  const isWindows =
    typeof navigator !== "undefined" &&
    navigator.userAgent.toLowerCase().includes("windows");
  const platformKey = isWindows ? "win" : "mac";
  const configured = asRecord(asRecord(config).runtime_components);
  const nestedRuntime = asRecord(runtime.runtime);
  const installed = asRecord(
    runtime.installed_versions || nestedRuntime.installed_versions,
  );

  return Object.keys(componentNames).map((key) => {
    const componentConfig = asRecord(configured[key]);
    const asset = asRecord(
      componentConfig[platformKey] ||
        componentConfig[isWindows ? "windows" : "macos"],
    );
    const local = asRecord(installed[key]);
    const statusKey = componentStatusKeys[key] || `${key}_installed`;
    const statusFlag = runtime[statusKey] ?? nestedRuntime[statusKey];
    const pathKey = `${key.replace("_runtime", "")}_path`;
    const path =
      textValue(runtime[pathKey]) ||
      textValue(nestedRuntime[pathKey]) ||
      (key === "node_worker"
        ? textValue(runtime.worker_entry) ||
          textValue(nestedRuntime.worker_entry)
        : "");
    return {
      key,
      name: componentNames[key],
      required: key !== "ocr",
      bundled: key === "node_worker",
      installed:
        statusFlag !== undefined
          ? Boolean(statusFlag)
          : Boolean(textValue(local.version) || path),
      configVersion: textValue(asset.version),
      installedVersion: textValue(local.version),
      url: textValue(asset.url),
      note:
        key === "node_worker"
          ? "随本地程序安装包内置，不需要单独安装。"
          : textValue(asset.note) ||
            textValue(asset.description) ||
            "",
      path,
    };
  });
}

/** asRecord 把未知接口数据安全转换为可读取对象。 */
function asRecord(value: unknown): UnknownRecord {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  return value as UnknownRecord;
}

/** textValue 把未知字段安全转换为去除首尾空格的字符串。 */
function textValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}
