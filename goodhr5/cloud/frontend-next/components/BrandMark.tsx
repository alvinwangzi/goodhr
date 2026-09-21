/** 本文件负责展示 HR Plus 品牌标识。 */
import { Box, Typography } from "@mui/material";

/** BrandMark 输出可点击的 HR Plus 品牌标识。 */
export default function BrandMark() {
  return (
    <Box component="a" href="/" sx={{ display: "inline-flex", alignItems: "center", gap: 1.25 }}>
      <Box
        component="img"
        src="/brand/goodhr-logo-transparent-512.png"
        alt="HR Plus"
        sx={{
          width: 38,
          height: 38,
          display: "block",
          objectFit: "contain",
          flexShrink: 0,
        }}
      />
      <Typography sx={{ fontSize: 21, fontWeight: 800, color: "text.primary" }}>HR Plus</Typography>
    </Box>
  );
}
