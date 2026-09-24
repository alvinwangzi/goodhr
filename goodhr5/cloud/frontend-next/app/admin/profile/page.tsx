/** 本文件负责新版后台个人信息页面，允许用户修改昵称和密码。 */
"use client";

import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
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

  /* ── 密码相关状态 ── */
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showOld, setShowOld] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [pwSaving, setPwSaving] = useState(false);

  /** 初始化时从用户信息中读取昵称。 */
  useEffect(() => {
    if (user?.display_name !== undefined) {
      setDisplayName(user.display_name || "");
    }
  }, [user?.display_name]);

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

  /** changePassword 提交修改密码请求。 */
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

        {/* ── 密码修改区 ── */}
        <SectionPanel>
          <Typography sx={{ fontSize: 17, fontWeight: 760, mb: 2 }}>
            修改密码
          </Typography>
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
                      <IconButton
                        size="small"
                        onClick={() => setShowOld(!showOld)}
                        edge="end"
                      >
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
                      <IconButton
                        size="small"
                        onClick={() => setShowNew(!showNew)}
                        edge="end"
                      >
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
                      <IconButton
                        size="small"
                        onClick={() => setShowConfirm(!showConfirm)}
                        edge="end"
                      >
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
        </SectionPanel>
      </Box>
    </>
  );
}
