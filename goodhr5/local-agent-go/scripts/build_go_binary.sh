#!/usr/bin/env bash
# 文件作用：编译 HRPlus Go 本地程序可执行文件，供发布包或安装器使用。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_OS="${TARGET_OS:-$(go env GOOS)}"
TARGET_ARCH="${TARGET_ARCH:-$(go env GOARCH)}"
BUILD_ENV="${GOODHR_APP_ENV:-}"
VERSION="${VERSION:-0.1.2}"
CONFIG_FILE="${GOODHR_BUILD_CONFIG:-$ROOT_DIR/packaging/environments/$BUILD_ENV.json}"
if [[ "$CONFIG_FILE" != /* ]]; then
  CONFIG_FILE="$PWD/$CONFIG_FILE"
fi

# log 输出脚本状态。
# 参数为要显示的中文消息。
log() {
  printf '[HRPlus] %s\n' "$*"
}

if [[ "$BUILD_ENV" != "dev" && "$BUILD_ENV" != "prod" ]]; then
  log "请设置 GOODHR_APP_ENV=dev 或 GOODHR_APP_ENV=prod 后再构建"
  exit 1
fi

log "开始编译 Go 本地程序：环境=$BUILD_ENV GOOS=$TARGET_OS GOARCH=$TARGET_ARCH"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=0 GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" go run \
    ./cmd/build-local-agent \
    -env "$BUILD_ENV" -config "$CONFIG_FILE" \
    -os "$TARGET_OS" -arch "$TARGET_ARCH" -version "$VERSION"
)
