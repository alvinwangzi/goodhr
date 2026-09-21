/** 本文件负责展示 HR Plus 登录页面，采用极光渐变背景 + 左右结构布局。 */
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import BrandMark from "@/components/BrandMark";
import LoginForm from "@/components/LoginForm";
import { Box, Container, Stack, Typography } from "@mui/material";

const loginPoints = ["简历数据存本地，隐私安全有保障","邮箱验证码登录，方便快捷"];

/** LoginPage 输出极光渐变背景 + 左文右表结构的登录界面。 */
export default function LoginPage() {
  return (
    <Box
      sx={{
        minHeight: "100vh",
        position: "relative",
        overflow: "hidden",
        background: "linear-gradient(135deg, #0a1628 0%, #0d2137 15%, #0f2d4a 30%, #0c3b6e 50%, #0a2d5c 65%, #0d1f3c 80%, #0a1628 100%)",
        backgroundSize: "400% 400%",
        animation: "auroraShift 18s ease infinite",
      }}
    >
      {/* 极光光斑 */}
      <Box sx={{ position: "absolute", top: "-15%", right: "-10%", width: "60vw", maxWidth: 900, aspectRatio: 1, borderRadius: "50%", background: "radial-gradient(circle, #1a6dd4 0%, transparent 70%)", filter: "blur(100px)", opacity: 0.35, animation: "auroraShift 22s ease-in-out infinite alternate", pointerEvents: "none" }} />
      <Box sx={{ position: "absolute", bottom: "-10%", left: "-8%", width: "50vw", maxWidth: 750, aspectRatio: 1, borderRadius: "50%", background: "radial-gradient(circle, #0052CC 0%, transparent 70%)", filter: "blur(100px)", opacity: 0.35, animation: "auroraShift 26s ease-in-out -8s infinite alternate", pointerEvents: "none" }} />

      {/* 内容层 */}
      <Box sx={{ position: "relative", zIndex: 1, minHeight: "100vh", display: "flex", flexDirection: "column" }}>
        {/* 顶部品牌 */}
        <Container maxWidth="lg" sx={{ py: 2.5 }}>
          <div className="login-fade-in">
            <Box sx={{ "& .MuiTypography-root": { color: "rgba(255,255,255,0.92) !important" } }}>
              <BrandMark />
            </Box>
          </div>
        </Container>

        {/* 左右结构主体 */}
        <Container maxWidth="lg" sx={{ flex: 1, display: "grid", alignItems: "center", py: { xs: 4, md: 6 } }}>
          <Box
            sx={{
              display: "grid",
              gridTemplateColumns: { xs: "1fr", md: "minmax(0, 1fr) 440px" },
              gap: { xs: 5, md: 10 },
              alignItems: "start",
            }}
          >
            {/* 左侧文案 */}
            <Box>
              <Typography
                component="h1"
                className="login-fade-in"
                sx={{
                  color: "#ffffff",
                  fontSize: { xs: 32, sm: 40, md: 50 },
                  lineHeight: 1.2,
                  fontWeight: 800,
                  letterSpacing: "-0.02em",
                }}
              >
                解放双手，<br />
                聚焦价值创造，<br/>
                聚焦有温度的交流
              </Typography>
              <Typography
                className="login-fade-in-delay"
                sx={{ mt: 3, color: "rgba(255,255,255,0.6)", fontSize: 20, lineHeight: 1.8, maxWidth: 480 }}
              >
                · 判断 
                · 共情 
                · 吸引 
                · 共赢
              </Typography>
              <Stack spacing={1.5} sx={{ mt: 4 }}>
                {loginPoints.map((point) => (
                  <Stack key={point} direction="row" spacing={1} sx={{ alignItems: "center" }}>
                    <CheckCircleRoundedIcon sx={{ color: "rgba(255,255,255,0.45)", fontSize: 18 }} />
                    <Typography sx={{ color: "rgba(255,255,255,0.55)", fontSize: 14 }}>{point}</Typography>
                  </Stack>
                ))}
              </Stack>
            </Box>

            {/* 右侧白色登录卡片 */}
            <Box
              className="login-fade-in-delay"
              sx={{
                p: { xs: 3, sm: 4 },
                bgcolor: "#ffffff",
                borderRadius: "24px",
                boxShadow: "0 24px 80px rgba(0, 0, 0, 0.25)",
              }}
            >
              <Typography component="h2" sx={{ color: "text.primary", fontSize: 24, fontWeight: 750, mb: 2 }}>
                欢迎登录
              </Typography>
              <LoginForm />
            </Box>
          </Box>
        </Container>
      </Box>
    </Box>
  );
}
