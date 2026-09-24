/** 本文件负责新版后台个人信息页面，允许用户修改昵称和密码。 */
"use client";

import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import VerifiedRoundedIcon from "@mui/icons-material/VerifiedRounded";
import Visibility from "@mui/icons-material/Visibility";
import VisibilityOff from "@mui/icons-material/VisibilityOff";
import {
  Alert,
  Box,
  Button,
  IconButton,
  InputAdornment,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import { useEffect, useState } from "react";
import { cloudRequest } from "@/lib/admin-api";
import { PageHeader, SectionPanel } from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";

/** ProfilePage 展示并允许用户修改昵称和登录密码。 */
export default function ProfilePage() {
  const { user, notify, refreshSession } = useAdmin();

  /* ── 昵称相关状态 ── */
  const [displayName, setDisplayName] = useState("");
  const [nameSaving, setNameSaving] = useState(false);

  /* ── 密码状态：是否有密码 ── */
  const [hasPassword, setHasPassword] = useState<boolean | undefined>(undefined);

  /* ── 修改密码（已有密码）── */
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showOld, setShowOld] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [pwSaving, setPwSaving] = useState(false);

  /* ── 设置密码（没有密码，走验证码）── */
  const [setCode, setSetCode] = useState("");
  const [setNewPw, setSetNewPw] = useState("");
  const [setConfirmPw, setSetConfirmPw] = useState("");
  const [showSetNew, setShowSetNew] = useState(false);
  const [showSetConfirm, setShowSetConfirm] = useState(false);
  const [settingPw, setSettingPw] = useState(false);
  const [codeCooldown, setCodeCooldown] = useState(0);
  const [codeSending, setCodeSending] = useState(false);

  /** 初始化时从用户信息中读取昵称，并查询密码设置状态。 */
  useEffect(() => {
    if (user?.display_name !== undefined) {
      setDisplayName(user.display_name || "");
    }
  }, [user?.display_name]);

  /** checkHasPassword 查询当前用户是否已设置密码。 */
  useEffect(() => {
    if (!user?.email) return;
    let cancelled = false;
    async function check() {
      try {
        const data = await cloudRequest(
          `/api/auth/login-status?email=${encodeURIComponent(user!.email)}`,
        );
        if (!cancelled) {
          setHasPassword(!!data?.status?.has_password);
        }
      } catch {
        if (!cancelled) setHasPassword(false);
      }
    }
    void check();
    return () => { cancelled = true; };
  }, [user?.email]);

  /** 验证码倒计时。 */
  useEffect(() => {
    if (codeCooldown <= 0) return;
    const timer = setInterval(() => setCodeCooldown((v) => v - 1), 1000);
    return () => clearInterval(timer);
  }, [codeCooldown]);

  /** saveName 保存昵称修改。 */
  async function saveName() {
    setNameSaving(true);
    try {
      await cloudRequest("/api/auth/update-profile", {
        method: "PUT",
        body: { display_name: displayName },
      });
      notify("昵称已更新", "success");
      await refreshSession();
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "昵称修改失败",
        "error",
      );
    } finally {
      setNameSaving(false);
    }
  }

  /** changePassword 已有密码的用户修改密码。 */
  async function changePassword() {
    if (!oldPassword) return notify("请输入当前密码", "warning");
    if (newPassword.length < 6) return notify("新密码至少要 6 位", "warning");
    if (newPassword !== confirmPassword)
      return notify("两次输入的新密码不一致", "warning");
    setPwSaving(true);
    try {
      await cloudRequest("/api/auth/change-password", {
        method: "POST",
        body: { old_password: oldPassword, new_password: newPassword },
      });
      notify("密码修改成功", "success");
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "密码修改失败",
        "error",
      );
    } finally {
      setPwSaving(false);
    }
  }

  /** sendSetCode 发送验证码用于首次设置密码。 */
  async function sendSetCode() {
    setCodeSending(true);
    try {
      await cloudRequest("/api/auth/send-code", {
        method: "POST",
        body: { email: user?.email || "" },
      });
      setCodeCooldown(60);
      notify("验证码已发送，请查看邮箱", "success");
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "验证码发送失败",
        "error",
      );
    } finally {
      setCodeSending(false);
    }
  }

  /** setPassword 没有密码的用户通过验证码首次设置密码。 */
  async function setPassword() {
    if (setCode.trim().length !== 4) return notify("请输入 4 位验证码", "warning");
    if (setNewPw.length < 6) return notify("密码至少要 6 位", "warning");
    if (setNewPw !== setConfirmPw) return notify("两次输入的密码不一致", "warning");
    setSettingPw(true);
    try {
      await cloudRequest("/api/auth/set-password", {
        method: "POST",
        body: {
          email: user?.email || "",
          code: setCode.trim(),
          password: setNewPw,
        },
      });
      notify("密码设置成功", "success");
      setSetCode("");
      setSetNewPw("");
      setSetConfirmPw("");
      setHasPassword(true);
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "密码设置失败",
        "error",
      );
    } finally {
      setSettingPw(false);
    }
  }

  return (
    <>
      <PageHeader
        title="个人信息"
        description="修改你的昵称和登录密码。"
      />

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", md: "repeat(2, minmax(0, 1fr))" },
          gap: 2,
          alignItems: "start",
        }}
      >
        {/* ── 昵称修改区 ── */}
        <SectionPanel>
          <Typography sx={{ fontSize: 17, fontWeight: 760, mb: 2 }}>
            昵称
          </Typography>
          <Stack spacing={2}>
            <TextField
              size="small"
              label="邮箱"
              value={user?.email || ""}
              disabled
              helperText="邮箱是登录账号，不能修改"
              fullWidth
            />
            <TextField
              size="small"
              label="昵称"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="给自己取个名字"
              fullWidth
            />
            <Button
              variant="contained"
              startIcon={<SaveRoundedIcon />}
              disabled={nameSaving}
              onClick={() => void saveName()}
            >
              {nameSaving ? "保存中" : "保存昵称"}
            </Button>
          </Stack>
        </SectionPanel>

        {/* ── 密码区 ── */}
        <SectionPanel>
          <Typography sx={{ fontSize: 17, fontWeight: 760, mb: 2 }}>
            {hasPassword === true ? "修改密码" : "设置密码"}
          </Typography>

          {hasPassword === undefined ? (
            <Typography color="text.secondary">正在检查密码状态...</Typography>
          ) : hasPassword ? (
            /* ── 已有密码：旧密码 + 新密码 ── */
            <Stack spacing={2}>
              <TextField
                size="small"
                type={showOld ? "text" : "password"}
                label="当前密码"
                value={oldPassword}
                onChange={(e) => setOldPassword(e.target.value)}
                disabled={pwSaving}
                fullWidth
                slotProps={{
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => setShowOld(!showOld)} edge="end">
                          {showOld ? <VisibilityOff /> : <Visibility />}
                        </IconButton>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <TextField
                size="small"
                type={showNew ? "text" : "password"}
                label="新密码"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                disabled={pwSaving}
                placeholder="至少 6 位"
                fullWidth
                slotProps={{
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => setShowNew(!showNew)} edge="end">
                          {showNew ? <VisibilityOff /> : <Visibility />}
                        </IconButton>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <TextField
                size="small"
                type={showConfirm ? "text" : "password"}
                label="确认新密码"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                disabled={pwSaving}
                fullWidth
                slotProps={{
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => setShowConfirm(!showConfirm)} edge="end">
                          {showConfirm ? <VisibilityOff /> : <Visibility />}
                        </IconButton>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <Button
                variant="contained"
                startIcon={<SaveRoundedIcon />}
                disabled={
                  pwSaving ||
                  !oldPassword ||
                  newPassword.length < 6 ||
                  newPassword !== confirmPassword
                }
                onClick={() => void changePassword()}
              >
                {pwSaving ? "修改中" : "修改密码"}
              </Button>
            </Stack>
          ) : (
            /* ── 没有密码：验证码 + 新密码 ── */
            <Stack spacing={2}>
              <Alert severity="info" sx={{ fontSize: 13 }}>
                你还没有设置密码，通过邮箱验证码来设一个吧
              </Alert>
              <TextField
                size="small"
                label="验证码"
                inputMode="numeric"
                placeholder="请输入邮箱中 4 位验证码"
                value={setCode}
                onChange={(e) => setSetCode(e.target.value.replace(/\D/g, "").slice(0, 4))}
                disabled={settingPw}
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
                          onClick={() => void sendSetCode()}
                          disabled={codeSending || codeCooldown > 0 || settingPw}
                          size="small"
                        >
                          {codeSending ? "发送中" : codeCooldown > 0 ? `${codeCooldown}s` : "发送验证码"}
                        </Button>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <TextField
                size="small"
                type={showSetNew ? "text" : "password"}
                label="新密码"
                value={setNewPw}
                onChange={(e) => setSetNewPw(e.target.value)}
                disabled={settingPw}
                placeholder="至少 6 位"
                fullWidth
                slotProps={{
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => setShowSetNew(!showSetNew)} edge="end">
                          {showSetNew ? <VisibilityOff /> : <Visibility />}
                        </IconButton>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <TextField
                size="small"
                type={showSetConfirm ? "text" : "password"}
                label="确认新密码"
                value={setConfirmPw}
                onChange={(e) => setSetConfirmPw(e.target.value)}
                disabled={settingPw}
                fullWidth
                slotProps={{
                  input: {
                    endAdornment: (
                      <InputAdornment position="end">
                        <IconButton size="small" onClick={() => setShowSetConfirm(!showSetConfirm)} edge="end">
                          {showSetConfirm ? <VisibilityOff /> : <Visibility />}
                        </IconButton>
                      </InputAdornment>
                    ),
                  },
                }}
              />
              <Button
                variant="contained"
                startIcon={<SaveRoundedIcon />}
                disabled={
                  settingPw ||
                  setCode.trim().length !== 4 ||
                  setNewPw.length < 6 ||
                  setNewPw !== setConfirmPw
                }
                onClick={() => void setPassword()}
              >
                {settingPw ? "设置中" : "设置密码"}
              </Button>
            </Stack>
          )}
        </SectionPanel>
      </Box>
    </>
  );
}
