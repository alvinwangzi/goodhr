/** 本文件负责定义 HR Plus 新版前端的 MUI 明亮主题，全站统一使用一套蓝色主题。 */
"use client";

import { createTheme } from "@mui/material/styles";

/** createGoodHRTheme 生成统一的浅色蓝色主题。 */
export function createGoodHRTheme() {
  return createTheme({
    palette: {
      mode: "light",
      primary: {
        main: "#0052CC",
        dark: "#003D99",
        light: "#EBF0FF",
        contrastText: "#ffffff",
      },
      secondary: { main: "#1A1F36" },
      background: { default: "#F5F7FF", paper: "#ffffff" },
      text: { primary: "#1A1F36", secondary: "#5E6580" },
      divider: "#D6DCF0",
      action: { hover: "#EEF1FF", selected: "#EBF0FF" },
      success: {
        main: "#238653",
        dark: "#17633d",
        light: "#eaf5ee",
        contrastText: "#ffffff",
      },
      warning: { main: "#c47a1a" },
      error: { main: "#c83f49" },
    },
    shape: { borderRadius: 8 },
    typography: {
      fontFamily:
        'Inter, "SF Pro Display", "PingFang SC", "Microsoft YaHei", Arial, sans-serif',
      button: { textTransform: "none", fontWeight: 700, letterSpacing: 0 },
      h1: { fontWeight: 760, letterSpacing: 0 },
      h2: { fontWeight: 720, letterSpacing: 0 },
      h3: { fontWeight: 700, letterSpacing: 0 },
    },
    components: {
      MuiButton: {
        styleOverrides: {
          root: {
            minHeight: 44,
            borderRadius: 999,
            boxShadow: "none",
            paddingInline: 20,
          },
        },
      },
      MuiPaper: {
        styleOverrides: { root: { backgroundImage: "none" } },
      },
      MuiTextField: {
        defaultProps: { variant: "outlined" },
      },
      MuiOutlinedInput: {
        styleOverrides: {
          root: { minHeight: 56, borderRadius: 18 },
        },
      },
    },
  });
}

export default createGoodHRTheme();
