#!/bin/sh
# 本文件用于备份 HRPlus 5 生产环境的 PostgreSQL 数据库到压缩 SQL 文件，可放进 crontab 定时执行。
# 备份通过 docker exec 进入 postgres 容器执行 pg_dump，无需在宿主机安装 psql。
#
# 用法（在 goodhr5/ 目录下）：
#   sh scripts/backup-db.sh
# 可用环境变量覆盖默认值：
#   BACKUP_DIR  备份输出目录，默认 goodhr5/backups
#   KEEP_DAYS   保留天数，超过则删除，默认 14 天
set -eu

# 定位到 goodhr5/ 目录（脚本位于 goodhr5/scripts/ 下）。
PROJECT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
BACKUP_DIR="${BACKUP_DIR:-$PROJECT_DIR/backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"

# 与 docker-compose.prod.yml 的 name(goodhr5-prod) + 服务名(postgres) 对应。
CONTAINER="${POSTGRES_CONTAINER:-goodhr5-prod-postgres-1}"

# 从容器内读取真实账号库名，避免与 .env 不一致导致备份失败。
DB_USER="${POSTGRES_USER:-$(docker exec "$CONTAINER" printenv POSTGRES_USER)}"
DB_NAME="${POSTGRES_DB:-$(docker exec "$CONTAINER" printenv POSTGRES_DB)}"

TIMESTAMP="$(date '+%Y%m%d_%H%M%S')"
OUT_FILE="$BACKUP_DIR/goodhr5_${DB_NAME}_${TIMESTAMP}.sql.gz"

mkdir -p "$BACKUP_DIR"

echo "开始备份数据库 $DB_NAME（容器 $CONTAINER）..."
docker exec "$CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" | gzip > "$OUT_FILE"
echo "备份完成：$OUT_FILE"

# 清理超过保留天数的旧备份。
find "$BACKUP_DIR" -name "goodhr5_${DB_NAME}_*.sql.gz" -mtime +"$KEEP_DAYS" -delete
echo "已清理超过 $KEEP_DAYS 天的旧备份。"
