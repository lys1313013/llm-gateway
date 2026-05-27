#!/usr/bin/env bash
#
# LLM Gateway 数据库迁移脚本
# 用途：将 PostgreSQL 从统一的 docker-compose.yml 拆分到独立的 docker-compose.db.yml
# 流程：备份 → 停止旧服务 → 启动新 DB → 验证 → 启动应用 → 清理
#
set -euo pipefail

# ============================================================
# 配置
# ============================================================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

BACKUP_DIR="${SCRIPT_DIR}/backups"
BACKUP_FILE="${BACKUP_DIR}/llm_gateway_backup_$(date +%Y%m%d_%H%M%S).sql"
DB_CONTAINER="llm_gateway_postgres"
DB_NAME="mock_openai"
DB_USER="postgres"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
success() { echo -e "${GREEN}[OK]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*"; }

# ============================================================
# Step 0: 前置检查
# ============================================================
echo ""
echo "=========================================="
echo "  LLM Gateway 数据库拆分迁移"
echo "=========================================="
echo ""

# 检查 docker 和 docker compose
if ! command -v docker &>/dev/null; then
    error "docker 未安装"
    exit 1
fi

# 检查旧 compose 是否正在运行
info "检查当前服务状态..."
if docker compose ps --format json 2>/dev/null | grep -q "running"; then
    success "检测到正在运行的服务"
else
    warn "未检测到运行中的服务，将尝试直接迁移"
fi

# ============================================================
# Step 1: 备份数据库
# ============================================================
echo ""
info "=========================================="
info "Step 1/6: 备份数据库"
info "=========================================="

mkdir -p "$BACKUP_DIR"

# 检查 PG 容器是否在运行
if docker ps --format '{{.Names}}' | grep -q "^${DB_CONTAINER}$"; then
    info "正在从运行中的容器备份..."
    docker exec "$DB_CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" --clean --if-exists > "$BACKUP_FILE"
else
    warn "PG 容器未运行，尝试先启动旧服务..."
    docker compose up -d postgres 2>/dev/null || true
    info "等待 PG 就绪..."
    sleep 5
    docker exec "$DB_CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" --clean --if-exists > "$BACKUP_FILE"
fi

BACKUP_SIZE=$(wc -c < "$BACKUP_FILE" | tr -d ' ')
if [ "$BACKUP_SIZE" -gt 0 ]; then
    success "备份完成: ${BACKUP_FILE} (${BACKUP_SIZE} bytes)"
else
    error "备份文件为空！中止迁移。"
    exit 1
fi

# 统计备份中的表数据（pg_dump 默认使用 COPY 格式）
info "备份内容概览："
for table in api_logs provider model_route exposed_model; do
    count=$(grep -c "COPY public.${table}" "$BACKUP_FILE" 2>/dev/null || echo "0")
    info "  - ${table}: ${count} 个 COPY 段"
done

# ============================================================
# Step 2: 停止旧的统一 compose 服务
# ============================================================
echo ""
info "=========================================="
info "Step 2/6: 停止旧服务"
info "=========================================="

info "停止所有旧容器（保留数据卷）..."
docker compose down --remove-orphans

# 如果 PG 容器仍在运行（因已从 compose 文件中移除），手动停止
if docker ps --format '{{.Names}}' | grep -q "^${DB_CONTAINER}$"; then
    info "停止残留的 PostgreSQL 容器..."
    docker stop "$DB_CONTAINER" && docker rm "$DB_CONTAINER"
fi

success "旧服务已停止"

# ============================================================
# Step 3: 使用新的独立 DB compose 启动 PostgreSQL
# ============================================================
echo ""
info "=========================================="
info "Step 3/6: 启动独立 PostgreSQL 服务"
info "=========================================="

info "使用 docker-compose.db.yml 启动 PostgreSQL..."
docker compose -f docker-compose.db.yml up -d

info "等待 PostgreSQL 健康检查通过..."
RETRIES=30
while [ $RETRIES -gt 0 ]; do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' "$DB_CONTAINER" 2>/dev/null || echo "unknown")
    if [ "$STATUS" = "healthy" ]; then
        success "PostgreSQL 已就绪"
        break
    fi
    RETRIES=$((RETRIES - 1))
    sleep 2
done

if [ $RETRIES -eq 0 ]; then
    error "PostgreSQL 启动超时，请检查日志: docker compose -f docker-compose.db.yml logs postgres"
    exit 1
fi

# ============================================================
# Step 4: 验证数据完整性
# ============================================================
echo ""
info "=========================================="
info "Step 4/6: 验证数据完整性"
info "=========================================="

# 检查所有表是否存在且可查询
VERIFY_PASS=true
for table in api_logs provider model_route exposed_model; do
    CURRENT_COUNT=$(docker exec "$DB_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -t -c "SELECT COUNT(*) FROM ${table};" 2>/dev/null | tr -d ' ')

    if [ -n "$CURRENT_COUNT" ]; then
        success "${table}: ${CURRENT_COUNT} 条记录 ✓"
    else
        error "${table}: 查询失败！"
        VERIFY_PASS=false
    fi
done

if [ "$VERIFY_PASS" = false ]; then
    error "数据验证失败！请检查后决定是否继续。"
    error "可使用备份恢复: docker exec -i ${DB_CONTAINER} psql -U ${DB_USER} -d ${DB_NAME} < ${BACKUP_FILE}"
    echo ""
    read -rp "是否强制继续？(y/N) " -n 1
    echo ""
    if [[ ! "$REPLY" =~ ^[Yy]$ ]]; then
        error "用户取消，中止迁移。"
        exit 1
    fi
else
    success "数据完整性验证通过"
fi

# ============================================================
# Step 5: 启动应用服务
# ============================================================
echo ""
info "=========================================="
info "Step 5/6: 启动应用服务"
info "=========================================="

info "启动 backend 和 frontend..."
docker compose up -d --force-recreate

info "等待服务就绪..."
sleep 8

# 检查容器状态
ALL_OK=true
for container in llm_gateway_backend llm_gateway_frontend; do
    STATUS=$(docker inspect --format='{{.State.Status}}' "$container" 2>/dev/null || echo "unknown")
    if [ "$STATUS" = "running" ]; then
        success "${container}: ${STATUS} ✓"
    else
        error "${container}: ${STATUS}"
        ALL_OK=false
    fi
done

if [ "$ALL_OK" = true ]; then
    success "所有服务已启动"
else
    error "部分服务启动失败，请检查日志: docker compose logs"
fi

# ============================================================
# Step 6: 清理确认
# ============================================================
echo ""
info "=========================================="
info "Step 6/6: 清理确认"
info "=========================================="

success "迁移完成！"
echo ""
echo "当前服务状态："
echo ""
echo "  数据库 (独立):"
docker compose -f docker-compose.db.yml ps --format "    {{.Name}}: {{.Status}}"
echo ""
echo "  应用服务:"
docker compose ps --format "    {{.Name}}: {{.Status}}"
echo ""

echo "=========================================="
echo "  迁移总结"
echo "=========================================="
echo ""
echo "  备份文件: ${BACKUP_FILE}"
echo ""
echo "  日常操作："
echo "    启动数据库:  docker compose -f docker-compose.db.yml up -d"
echo "    启动应用:    docker compose up -d"
echo "    停止应用:    docker compose down"
echo "    停止数据库:  docker compose -f docker-compose.db.yml down"
echo ""
echo "  连接自建 PG:  DB_HOST=<你的PG地址> docker compose up -d"
echo ""

read -rp "是否删除备份文件？(y/N) " -n 1
echo ""
if [[ "$REPLY" =~ ^[Yy]$ ]]; then
    rm -f "$BACKUP_FILE"
    success "备份文件已删除"
else
    success "备份文件已保留: ${BACKUP_FILE}"
fi

echo ""
success "全部完成！"
