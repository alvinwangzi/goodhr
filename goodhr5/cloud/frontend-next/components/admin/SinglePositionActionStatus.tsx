/** 本文件展示 HRPlus 单岗位当前动作和本次消息统计，不在页面开启浏览器动作或计时任务。 */
"use client";

import { Stack, Typography } from "@mui/material";
import { checkTimeLabel, currentActionLabel, type ActionDispatchStatus, type ReGreetStats } from "@/lib/single-position-actions";

/** SinglePositionActionStatus 显示本地检查时间和独立复打数量，结束后不显示未来等待。 */
export default function SinglePositionActionStatus({ dispatch, reGreet, running }: { dispatch?: ActionDispatchStatus; reGreet?: ReGreetStats; running: boolean }) {
  if (!dispatch && !reGreet) return null;
  return <Stack spacing={0.35} sx={{ mt: 0.5 }}>
    {dispatch ? <Typography sx={{ color: "text.secondary", fontSize: 13 }}>{running ? `当前：${currentActionLabel(dispatch.currentAction)}` : "本次运行已结束"}{dispatch.messagesEnabled ? <> · {dispatch.prioritizeReply ? "优先回复" : "按间隔检查消息"} · {dispatch.lastMessageCheck ? `最近检查 ${checkTimeLabel(dispatch.lastMessageCheck)}` : "本轮尚未检查消息"}</> : null}</Typography> : null}
    {dispatch && running && dispatch.waitingForCheck ? <Typography sx={{ color: "text.secondary", fontSize: 12 }}>下次消息检查 {checkTimeLabel(dispatch.nextMessageCheck)}</Typography> : null}
    {reGreet ? <Typography sx={{ color: "text.secondary", fontSize: 13 }}>复打招呼（成功 {reGreet.sent} · 跳过 {reGreet.skipped} · 失败 {reGreet.failed} · 待核对 {reGreet.unknown}）</Typography> : null}
  </Stack>;
}
