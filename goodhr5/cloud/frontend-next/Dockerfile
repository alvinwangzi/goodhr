# 本文件负责构建并运行 HRPlus 新版 Next.js 前端。
FROM node:22-alpine AS dependencies
RUN npm config set registry https://registry.npmmirror.com
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:22-alpine AS builder
# 构建期注入的公开地址：NEXT_PUBLIC_SITE_URL 用于 sitemap/SEO/邀请链接，
# NEXT_PUBLIC_CLOUD_API_BASE 留空时前端走同域相对路径 /api/。默认空值等同原有行为。
ARG NEXT_PUBLIC_SITE_URL=""
ARG NEXT_PUBLIC_CLOUD_API_BASE=""
ENV NEXT_PUBLIC_SITE_URL=$NEXT_PUBLIC_SITE_URL
ENV NEXT_PUBLIC_CLOUD_API_BASE=$NEXT_PUBLIC_CLOUD_API_BASE
WORKDIR /app
COPY --from=dependencies /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
COPY --from=builder /app/public ./public
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
EXPOSE 3000
CMD ["node", "server.js"]
