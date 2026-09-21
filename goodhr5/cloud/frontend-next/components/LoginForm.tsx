/** 本文件负责登录表单，支持邮箱验证码登录和密码登录两种方式，以及首次协议确认和设置密码。 */
"use client";

import LockRoundedIcon from "@mui/icons-material/LockRounded";
import MailOutlineRoundedIcon from "@mui/icons-material/MailOutlineRounded";
import VerifiedRoundedIcon from "@mui/icons-material/VerifiedRounded";
import Visibility from "@mui/icons-material/Visibility";
import VisibilityOff from "@mui/icons-material/VisibilityOff";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  InputAdornment,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useMemo, useState, type UIEvent } from "react";
import {
  apiRequest,
  INVITE_CACHE_KEY,
  legacyAdminURL,
  SESSION_EMAIL_KEY,
  TOKEN_KEY,
} from "@/lib/api";
import { captureLocalAgentPortFromURL } from "@/lib/admin-api";

const AGREEMENT_MARKDOWN = `## HR Plus 用户协议与隐私说明

** HR Plus 是效率工具，不是平台安全保险。继续使用前，请确认你能接受下面这些边界。**

### **1. 招聘平台账号风险自担**

HR Plus 会模拟人工操作招聘平台，但不保证账号永远安全、不被限制、不被封禁。

如果你认为招聘平台账号被限制、封号、降权、风控的后果自己承担不起，请不要使用本软件。

因招聘平台规则变化、风控策略、用户使用频率、账号状态等原因造成的账号异常、封禁、损失，由使用者自行承担。

### **2. 仅可用于合法招聘工作**

HR Plus 只能用于合法、合规的招聘、候选人沟通和团队协作。

不得用于诈骗、骚扰、倒卖简历、爬取数据、侵犯隐私、恶意营销、违法犯罪或其它不正当用途。

如果因为你的使用方式产生纠纷、投诉、处罚或法律后果，由你自行承担。

### **3. 简历与候选人信息属于高度敏感信息**

使用过程中，HR Plus 可能会读取或处理候选人的姓名、手机号、微信、工作经历、教育经历、求职意向、简历内容、沟通记录等信息。

这些信息仅用于帮助你完成招聘筛选、AI 判断、AI 回复、候选人跟进和团队内协作。

除岗位创建人、所属团队成员及你授权的成员外，其他用户不可见。

### **4. AI 会使用候选人信息辅助回复**

当你开启 AI 筛选、AI 回复、简历结构化等功能时，系统可能会把必要的候选人信息发送给 AI 服务，用来生成更准确的分析、评分、回复建议或结构化结果。

我们不会把这些信息用于与招聘无关的用途。

### **5. 营销与服务通知**

你同意我们通过你提供的邮箱发送必要的服务通知，例如验证码、订单、会员到期、本地程序更新、风险提醒等。

我们也可能发送少量营销邮件，例如套餐优惠、功能更新、召回提醒等。你可以联系我取消接收营销类邮件。

### **6. 请自行控制使用频率**

HR Plus 提供了模拟休息、打开概率、操作间隔等配置，但这些配置不能保证完全规避招聘平台风控。

请根据自己的账号情况谨慎设置，不要高频、异常、批量地使用。

### **7. 数据可见范围**

你的岗位、任务、团队、简历库数据，只对你和你所在团队内有权限的成员可见。

本地浏览器数据、招聘平台登录状态、截图缓存等敏感数据主要保存在你的本地电脑中。

### **8. 继续使用代表你已理解并同意**

如果你勾选并继续登录，表示你已经阅读、理解并同意以上内容。

如果你不同意，或者无法承担相关风险，请停止使用 HR Plus。`;

/** LoginForm 提供验证码登录和密码登录两种方式，以及协议确认和设置密码功能。 */
export default function LoginForm() {
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [loading, setLoading] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [agreementOpen, setAgreementOpen] = useState(false);
  const [agreementScrolled, setAgreementScrolled] = useState(false);
  const [agreementChecked, setAgreementChecked] = useState(false);
  const [acceptedEmail, setAcceptedEmail] = useState("");

  /* ── 密码登录相关状态 ── */
  const [activeTab, setActiveTab] = useState(0);
  const [hasPassword, setHasPassword] = useState(false);
  const [isLocked, setIsLocked] = useState(false);
  const [lockRemainingSec, setLockRemainingSec] = useState(0);

  /* ── 设置密码对话框 ── */
  const [setPwOpen, setSetPwOpen] = useState(false);
  const [pwCode, setPwCode] = useState("");
  const [pwNew, setPwNew] = useState("");
  const [pwShow, setPwShow] = useState(false);
  const [pwCooldown, setPwCooldown] = useState(0);
  const [pwLoading, setPwLoading] = useState(false);
  const [pwError, setPwError] = useState("");

  const normalizedEmail = useMemo(() => email.trim().toLowerCase(), [email]);
  const agreementReady = acceptedEmail === normalizedEmail || agreementChecked;

  /* ── 初始化：读取缓存邮箱、检查登录状态 ── */
  useEffect(() => {
    captureLocalAgentPortFromURL();
    const cachedEmail = (localStorage.getItem(SESSION_EMAIL_KEY) || "")
      .trim()
      .toLowerCase();
    if (cachedEmail) setEmail(cachedEmail);
    const token = localStorage.getItem(TOKEN_KEY) || "";
    if (token) window.location.replace(resolveNextPath());
  }, []);

  /* ── 验证码发送倒计时 ── */
  useEffect(() => {
    if (cooldown <= 0) return undefined;
    const timer = window.setInterval(
      () => setCooldown((value) => Math.max(0, value - 1)),
      1000,
    );
    return () => window.clearInterval(timer);
  }, [cooldown]);

  /* ── 设置密码验证码倒计时 ── */
  useEffect(() => {
    if (pwCooldown <= 0) return undefined;
    const timer = window.setInterval(
      () => setPwCooldown((value) => Math.max(0, value - 1)),
      1000,
    );
    return () => window.clearInterval(timer);
  }, [setPwCooldown]);

  /* ── 锁定倒计时 ── */
  useEffect(() => {
    if (!isLocked || lockRemainingSec <= 0) {
      if (isLocked && lockRemainingSec <= 0) setIsLocked(false);
      return undefined;
    }
    const timer = window.setInterval(
      () => setLockRemainingSec((v) => Math.max(0, v - 1)),
      1000,
    );
    return () => window.clearInterval(timer);
  }, [isLocked, lockRemainingSec]);

  /* ── 邮箱变化时重置协议状态 ── */
  useEffect(() => {
    setAgreementChecked(false);
    setAgreementScrolled(false);
    setAcceptedEmail((current) => (current === normalizedEmail ? current : ""));
  }, [normalizedEmail]);

  /** checkPasswordStatus 查询邮箱的密码设置和锁定状态。 */
  async function checkPasswordStatus() {
    if (!normalizedEmail) return;
    try {
      const data = await apiRequest(
        `/api/auth/login-status?email=${encodeURIComponent(normalizedEmail)}`,
        { method: "GET" },
      );
      const status = data.status as any;
      if (status) {
        setHasPassword(!!status.has_password);
        if (status.is_locked) {
          setIsLocked(true);
          setLockRemainingSec(status.lock_remaining_sec || 0);
        } else {
          setIsLocked(false);
          setLockRemainingSec(0);
        }
      }
    } catch {
      /* 查询失败不阻塞登录流程 */
    }
  }

  /** handleTabChange 切换登录方式，密码登录时自动查询状态。 */
  function handleTabChange(_event: unknown, newValue: number) {
    setActiveTab(newValue);
    setError("");
    setMessage("");
    if (newValue === 1 && normalizedEmail) {
      void checkPasswordStatus();
    }
  }

  /** loginPassword 提交邮箱 + 密码登录。 */
  async function loginPassword() {
    setError("");
    setMessage("");
    if (!normalizedEmail) return setError("请先填写邮箱");
    if (!password) return setError("请输入密码");
    if (isLocked) return setError(`密码登录已锁定，请等待 ${formatCountdown(lockRemainingSec)}`);

    setLoading(true);
    try {
      if (!(await ensureAgreementAccepted())) return;

      const inviterID = localStorage.getItem(INVITE_CACHE_KEY) || "";
      const data = await apiRequest("/api/auth/login-password", {
        method: "POST",
        body: JSON.stringify({
          email: normalizedEmail,
          password,
          inviter_id: inviterID,
          agreement_accepted: agreementChecked,
        }),
      });

      const token = String(data.access_token || "");
      if (!token) throw new Error("登录成功但未返回登录凭证");
      localStorage.setItem(TOKEN_KEY, token);
      localStorage.setItem(SESSION_EMAIL_KEY, normalizedEmail);
      localStorage.removeItem(INVITE_CACHE_KEY);
      setMessage("登录成功，正在进入控制台");
      window.location.assign(resolveNextPath());
    } catch (requestError) {
      const msg = errorMessage(requestError);
      setError(msg);
      /* 密码错误时更新失败计数和锁定状态 */
      if (msg.includes("还可尝试")) {
        const match = msg.match(/还可尝试(\d)次/);
        if (match) {
          /* 保持密码登录可用，仅显示剩余次数 */
        }
      }
      if (msg.includes("锁定") || msg.includes("次数过多")) {
        setIsLocked(true);
        setLockRemainingSec(300);
      }
    } finally {
      setLoading(false);
    }
  }

  /** sendCode 请求向当前邮箱发送登录验证码。 */
  async function sendCode() {
    setError("");
    setMessage("");
    if (!normalizedEmail) return setError("请先填写邮箱");
    setLoading(true);
    try {
      await apiRequest("/api/auth/send-code", {
        method: "POST",
        body: JSON.stringify({ email: normalizedEmail }),
      });
      setCooldown(60);
      setMessage("验证码已发送，请查看邮箱");
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setLoading(false);
    }
  }

  /** ensureAgreementAccepted 确认当前邮箱是否已经完成协议确认。 */
  async function ensureAgreementAccepted() {
    if (acceptedEmail === normalizedEmail || agreementChecked) return true;
    const data = await apiRequest(
      `/api/auth/agreement-status?email=${encodeURIComponent(normalizedEmail)}`,
      { method: "GET" },
    );
    if (data.agreement_accepted) {
      setAcceptedEmail(normalizedEmail);
      return true;
    }
    setAgreementOpen(true);
    setError("首次登录前需要先读完并同意协议。");
    return false;
  }

  /** login 提交邮箱验证码并保存登录凭证。 */
  async function login() {
    setError("");
    setMessage("");
    if (!normalizedEmail) return setError("请先填写邮箱");
    if (code.trim().length !== 4) return setError("请输入 4 位验证码");
    setLoading(true);
    try {
      if (!(await ensureAgreementAccepted())) return;
      const inviterID = localStorage.getItem(INVITE_CACHE_KEY) || "";
      const data = await apiRequest("/api/auth/login", {
        method: "POST",
        body: JSON.stringify({
          email: normalizedEmail,
          code: code.trim(),
          inviter_id: inviterID,
          agreement_accepted: agreementChecked,
        }),
      });
      const token = String(data.access_token || "");
      if (!token) throw new Error("登录成功但未返回登录凭证");
      localStorage.setItem(TOKEN_KEY, token);
      localStorage.setItem(SESSION_EMAIL_KEY, normalizedEmail);
      localStorage.removeItem(INVITE_CACHE_KEY);
      setMessage("登录成功，正在进入控制台");
      window.location.assign(resolveNextPath());
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setLoading(false);
    }
  }

  /** handleOpenSetPassword 打开设置密码对话框，先发送验证码。 */
  async function handleOpenSetPassword() {
    if (!normalizedEmail) {
      setError("请先填写邮箱");
      return;
    }
    setPwError("");
    setPwCode("");
    setPwNew("");
    setSetPwOpen(true);
    /* 自动发送验证码 */
    try {
      await apiRequest("/api/auth/send-code", {
        method: "POST",
        body: JSON.stringify({ email: normalizedEmail }),
      });
      setPwCooldown(60);
    } catch (requestError) {
      setPwError(errorMessage(requestError));
    }
  }

  /** sendSetPwCode 在设置密码对话框中重新发送验证码。 */
  async function sendSetPwCode() {
    setPwError("");
    try {
      await apiRequest("/api/auth/send-code", {
        method: "POST",
        body: JSON.stringify({ email: normalizedEmail }),
      });
      setPwCooldown(60);
    } catch (requestError) {
      setPwError(errorMessage(requestError));
    }
  }

  /** submitSetPassword 提交验证码和新密码。 */
  async function submitSetPassword() {
    setPwError("");
    if (pwCode.trim().length !== 4) return setPwError("请输入 4 位验证码");
    if (pwNew.length < 6) return setPwError("密码至少要 6 位");
    setPwLoading(true);
    try {
      await apiRequest("/api/auth/set-password", {
        method: "POST",
        body: JSON.stringify({
          email: normalizedEmail,
          code: pwCode.trim(),
          password: pwNew,
        }),
      });
      setSetPwOpen(false);
      setHasPassword(true);
      setMessage("密码设置好了，现在可以用密码登录了");
    } catch (requestError) {
      setPwError(errorMessage(requestError));
    } finally {
      setPwLoading(false);
    }
  }

  /** handleAgreementScroll 判断协议内容是否已经滚动到底部。 */
  function handleAgreementScroll(event: UIEvent<HTMLDivElement>) {
    const target = event.currentTarget;
    const reachedBottom =
      target.scrollTop + target.clientHeight >= target.scrollHeight - 8;
    if (reachedBottom) setAgreementScrolled(true);
  }

  /** acceptAgreement 在弹框内确认协议勾选。 */
  function acceptAgreement() {
    setAgreementChecked(true);
    setAcceptedEmail(normalizedEmail);
    setAgreementOpen(false);
    setError("");
  }

  return (
    <Box>
      {/* ── Tab 切换：验证码登录 / 密码登录 ── */}
      <Tabs
        value={activeTab}
        onChange={handleTabChange}
        sx={{
          mb: 2.5,
          minHeight: 36,
          "& .MuiTab-root": { minHeight: 36, py: 0.5, fontSize: 14, fontWeight: 600 },
        }}
      >
        <Tab label="验证码登录" />
        <Tab label="密码登录" />
      </Tabs>

      {/* ── 公共邮箱输入 ── */}
      <TextField
        label="邮箱"
        placeholder="请输入邮箱"
        type="email"
        autoComplete="email"
        value={email}
        onChange={(event) => setEmail(event.target.value)}
        disabled={loading || pwLoading}
        fullWidth
        sx={{ mb: 2.25 }}
        slotProps={{
          input: {
            startAdornment: (
              <InputAdornment position="start">
                <MailOutlineRoundedIcon color="action" />
              </InputAdornment>
            ),
          },
        }}
      />

      {/* ── Tab 0：验证码登录 ── */}
      {activeTab === 0 && (
        <Box
          component="form"
          onSubmit={(event) => {
            event.preventDefault();
            void login();
          }}
          noValidate
        >
          <Stack spacing={2.25}>
            <TextField
              label="验证码"
              inputMode="numeric"
              placeholder="请输入邮箱中4位验证码"
              autoComplete="one-time-code"
              value={code}
              onChange={(event) =>
                setCode(event.target.value.replace(/\D/g, "").slice(0, 4))
              }
              disabled={loading}
              fullWidth
              slotProps={{
                input: {
                  startAdornment: (
                    <InputAdornment position="start">
                      <VerifiedRoundedIcon color="action" />
                    </InputAdornment>
                  ),
                  endAdornment: (
                    <InputAdornment position="end">
                      <Button
                        onClick={() => void sendCode()}
                        disabled={loading || cooldown > 0 || !email.trim()}
                        size="small"
                      >
                        {cooldown > 0 ? `${cooldown}s 后重试` : "发送验证码"}
                      </Button>
                    </InputAdornment>
                  ),
                },
              }}
            />
            <Stack
              direction={{ xs: "column", sm: "row" }}
              spacing={1}
              sx={{ alignItems: { sm: "center" }, justifyContent: "space-between" }}
            >
              <Button
                size="small"
                onClick={() => {
                  setAgreementOpen(true);
                  setAgreementScrolled(false);
                }}
                sx={{
                  color: "primary.main",
                  fontWeight: 600,
                  border: "1px solid",
                  borderColor: "primary.main",
                  borderRadius: 2,
                  px: 2,
                  py: 0.5,
                  fontSize: 13,
                  "&:hover": { bgcolor: "primary.main", color: "#fff" },
                }}
              >
                请先阅读协议
              </Button>
            </Stack>
            <MessageArea error={error} message={message} />
            <Button
              type="submit"
              variant="contained"
              size="large"
              disabled={loading || !email.trim() || code.length !== 4}
            >
              {loading ? "正在处理" : "登录"}
            </Button>
          </Stack>
        </Box>
      )}

      {/* ── Tab 1：密码登录 ── */}
      {activeTab === 1 && (
        <Box
          component="form"
          onSubmit={(event) => {
            event.preventDefault();
            void loginPassword();
          }}
          noValidate
        >
          <Stack spacing={2.25}>
            {!hasPassword && !isLocked ? (
              /* 未设置密码 */
              <Alert severity="info" sx={{ width: "100%" }}>
                还没设置密码呢，
                <Box
                  component="span"
                  sx={{
                    color: "primary.main",
                    cursor: "pointer",
                    fontWeight: 600,
                    textDecoration: "underline",
                  }}
                  onClick={handleOpenSetPassword}
                >
                  点我设置
                </Box>
              </Alert>
            ) : isLocked ? (
              /* 已锁定 */
              <Alert severity="warning" sx={{ width: "100%" }}>
                密码错误太多次，先休息 {formatCountdown(lockRemainingSec)} 再试，或者先用验证码登录
              </Alert>
            ) : null}

            <TextField
              label="密码"
              type={showPassword ? "text" : "password"}
              placeholder="请输入密码"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              disabled={loading}
              fullWidth
              slotProps={{
                input: {
                  startAdornment: (
                    <InputAdornment position="start">
                      <LockRoundedIcon color="action" />
                    </InputAdornment>
                  ),
                  endAdornment: (
                    <InputAdornment position="end">
                      <IconButton
                        size="small"
                        onClick={() => setShowPassword((v) => !v)}
                        edge="end"
                      >
                        {showPassword ? <VisibilityOff /> : <Visibility />}
                      </IconButton>
                    </InputAdornment>
                  ),
                },
              }}
            />

            <Stack
              direction="row"
              spacing={1}
              sx={{ justifyContent: "space-between", alignItems: "center" }}
            >
              <Button
                size="small"
                onClick={() => {
                  setAgreementOpen(true);
                  setAgreementScrolled(false);
                }}
                sx={{
                  color: "primary.main",
                  fontWeight: 600,
                  border: "1px solid",
                  borderColor: "primary.main",
                  borderRadius: 2,
                  px: 2,
                  py: 0.5,
                  fontSize: 13,
                  "&:hover": { bgcolor: "primary.main", color: "#fff" },
                }}
              >
                请先阅读协议
              </Button>
              <Button
                size="small"
                onClick={handleOpenSetPassword}
                disabled={!normalizedEmail}
              >
                {hasPassword ? "忘记密码？" : "设置密码"}
              </Button>
            </Stack>

            <MessageArea error={error} message={message} />

            <Button
              type="submit"
              variant="contained"
              size="large"
              disabled={loading || !email.trim() || !password || isLocked}
            >
              {loading ? "正在处理" : "登录"}
            </Button>
          </Stack>
        </Box>
      )}

      {/* ── 协议对话框 ── */}
      <Dialog
        open={agreementOpen}
        onClose={() => setAgreementOpen(false)}
        fullWidth
        maxWidth="md"
      >
        <DialogTitle>HR Plus 使用协议与隐私说明</DialogTitle>
        <DialogContent
          dividers
          onScroll={handleAgreementScroll}
          sx={{ maxHeight: { xs: "62vh", md: "68vh" } }}
        >
          <AgreementMarkdown markdown={AGREEMENT_MARKDOWN} />
        </DialogContent>
        <DialogActions
          sx={{
            px: 3,
            py: 2,
            alignItems: { xs: "stretch", sm: "center" },
            flexDirection: { xs: "column", sm: "row" },
          }}
        >
          <FormControlLabel
            control={
              <Checkbox
                checked={agreementReady}
                disabled={!agreementScrolled && acceptedEmail !== normalizedEmail}
                onChange={(event) => setAgreementChecked(event.target.checked)}
              />
            }
            label={
              agreementScrolled || acceptedEmail === normalizedEmail
                ? "我已阅读并同意"
                : "请先滚动到最下面，我再让你勾"
            }
            sx={{ mr: "auto" }}
          />
          <Button onClick={() => setAgreementOpen(false)}>先不登录</Button>
          <Button
            variant="contained"
            disabled={!agreementReady}
            onClick={acceptAgreement}
          >
            同意并继续
          </Button>
        </DialogActions>
      </Dialog>

      {/* ── 设置密码对话框 ── */}
      <SetPasswordDialog
        open={setPwOpen}
        onClose={() => setSetPwOpen(false)}
        email={normalizedEmail}
        code={pwCode}
        onCodeChange={setPwCode}
        newPassword={pwNew}
        onNewPasswordChange={setPwNew}
        showPassword={pwShow}
        onToggleShow={() => setPwShow((v) => !v)}
        cooldown={pwCooldown}
        loading={pwLoading}
        error={pwError}
        onSendCode={() => void sendSetPwCode()}
        onSubmit={() => void submitSetPassword()}
      />
    </Box>
  );
}

/* ──────────────────────────────────────────────────────────── */
/* 以下为辅助组件和工具函数                                       */
/* ──────────────────────────────────────────────────────────── */

/** MessageArea 固定高度消息区域，显示错误或成功提示。 */
function MessageArea({ error, message }: { error: string; message: string }) {
  return (
    <Box sx={{ minHeight: 48, display: "flex", alignItems: "center" }}>
      {error ? <Alert severity="error" sx={{ width: "100%" }}>{error}</Alert> : null}
      {message ? <Alert severity="success" sx={{ width: "100%" }}>{message}</Alert> : null}
    </Box>
  );
}

/** SetPasswordDialog 设置或重置密码的对话框，通过验证码验证后设置新密码。 */
function SetPasswordDialog({
  open,
  onClose,
  email,
  code,
  onCodeChange,
  newPassword,
  onNewPasswordChange,
  showPassword,
  onToggleShow,
  cooldown,
  loading,
  error,
  onSendCode,
  onSubmit,
}: {
  open: boolean;
  onClose: () => void;
  email: string;
  code: string;
  onCodeChange: (v: string) => void;
  newPassword: string;
  onNewPasswordChange: (v: string) => void;
  showPassword: boolean;
  onToggleShow: () => void;
  cooldown: number;
  loading: boolean;
  error: string;
  onSendCode: () => void;
  onSubmit: () => void;
}) {
  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="xs">
      <DialogTitle>设置密码</DialogTitle>
      <DialogContent>
        <Stack spacing={2.5} sx={{ mt: 1 }}>
          <Typography variant="body2" color="text.secondary">
            给 {email} 设置一个密码，以后登录就更方便了
          </Typography>

          {/* 验证码输入 */}
          <TextField
            label="验证码"
            inputMode="numeric"
            placeholder="请输入邮箱中4位验证码"
            autoComplete="one-time-code"
            value={code}
            onChange={(event) =>
              onCodeChange(event.target.value.replace(/\D/g, "").slice(0, 4))
            }
            disabled={loading}
            fullWidth
            slotProps={{
              input: {
                startAdornment: (
                  <InputAdornment position="start">
                    <VerifiedRoundedIcon color="action" />
                  </InputAdornment>
                ),
                endAdornment: (
                  <InputAdornment position="end">
                    <Button
                      onClick={onSendCode}
                      disabled={loading || cooldown > 0}
                      size="small"
                    >
                      {cooldown > 0 ? `${cooldown}s` : "重新发送"}
                    </Button>
                  </InputAdornment>
                ),
              },
            }}
          />

          {/* 新密码输入 */}
          <TextField
            label="新密码"
            type={showPassword ? "text" : "password"}
            placeholder="至少 6 位"
            autoComplete="new-password"
            value={newPassword}
            onChange={(event) => onNewPasswordChange(event.target.value)}
            disabled={loading}
            fullWidth
            slotProps={{
              input: {
                startAdornment: (
                  <InputAdornment position="start">
                    <LockRoundedIcon color="action" />
                  </InputAdornment>
                ),
                endAdornment: (
                  <InputAdornment position="end">
                    <IconButton size="small" onClick={onToggleShow} edge="end">
                      {showPassword ? <VisibilityOff /> : <Visibility />}
                    </IconButton>
                  </InputAdornment>
                ),
              },
            }}
          />

          {error ? <Alert severity="error">{error}</Alert> : null}
        </Stack>
      </DialogContent>
      <DialogActions sx={{ px: 3, py: 2 }}>
        <Button onClick={onClose}>返回登录</Button>
        <Button
          variant="contained"
          onClick={onSubmit}
          disabled={loading || code.length !== 4 || newPassword.length < 6}
        >
          {loading ? "设置中" : "确认设置"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

/** AgreementMarkdown 将协议 Markdown 转换为轻量展示内容。 */
function AgreementMarkdown({ markdown }: { markdown: string }) {
  return (
    <Stack spacing={1.4}>
      {markdown.split("\n").map((line, index) => {
        const text = line.trim();
        if (!text) return <Box key={index} sx={{ height: 4 }} />;
        if (text.startsWith("## ")) {
          return (
            <Typography key={index} component="h2" sx={{ fontSize: 22, fontWeight: 820 }}>
              {stripMarkdown(text.replace(/^##\s+/, ""))}
            </Typography>
          );
        }
        if (text.startsWith("### ")) {
          return (
            <Typography key={index} component="h3" sx={{ mt: 1.4, fontSize: 17, fontWeight: 800 }}>
              {stripMarkdown(text.replace(/^###\s+/, ""))}
            </Typography>
          );
        }
        const strong = text.match(/^\*\*(.*)\*\*$/);
        return (
          <Typography
            key={index}
            sx={{
              color: strong ? "text.primary" : "text.secondary",
              fontWeight: strong ? 760 : 400,
              lineHeight: 1.75,
            }}
          >
            {stripMarkdown(strong?.[1] || text)}
          </Typography>
        );
      })}
    </Stack>
  );
}

/** stripMarkdown 去掉协议展示里少量 Markdown 强调符号。 */
function stripMarkdown(text: string) {
  return text.replace(/\*\*/g, "");
}

/** resolveNextPath 返回登录完成后的安全站内跳转地址。 */
function resolveNextPath() {
  const nextPath = new URLSearchParams(window.location.search).get("next");
  return nextPath?.startsWith("/") && !nextPath.startsWith("//")
    ? nextPath
    : legacyAdminURL();
}

/** formatCountdown 将秒数格式化为可读的倒计时文本。 */
function formatCountdown(totalSec: number) {
  if (totalSec <= 0) return "0 秒";
  const minutes = Math.floor(totalSec / 60);
  const seconds = totalSec % 60;
  if (minutes > 0 && seconds > 0) return `${minutes} 分 ${seconds} 秒`;
  if (minutes > 0) return `${minutes} 分钟`;
  return `${seconds} 秒`;
}

/** errorMessage 从未知异常中提取可展示的信息。 */
function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "没处理成功，问题不大，我们再试一次";
}
