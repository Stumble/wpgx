#!/bin/bash

# 测试模式切换脚本
# 用于演示和验证两种测试模式

set -e

echo "========================================="
echo "WPGX 测试框架 - 模式切换演示"
echo "========================================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function print_section() {
    echo ""
    echo -e "${BLUE}=========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}=========================================${NC}"
}

function print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

function print_info() {
    echo -e "${YELLOW}ℹ️  $1${NC}"
}

# 检查 Docker
print_section "检查 Docker 状态"
if docker ps > /dev/null 2>&1; then
    print_success "Docker 正在运行"
else
    echo "❌ Docker 未运行，请先启动 Docker"
    exit 1
fi

# 选择模式
echo ""
echo "请选择测试模式："
echo "1) 容器模式 (Testcontainers) - 推荐"
echo "2) 直连模式 (Direct Connection) - 需要手动启动 PostgreSQL"
echo "3) 两种模式都测试"
echo "4) 仅编译检查"
echo ""
read -p "请输入选项 (1-4): " choice

case $choice in
    1)
        print_section "运行容器模式测试"
        print_info "testcontainers 会自动启动和清理 PostgreSQL 容器"
        export WPGX_TEST_USE_CONTAINER=true
        export ENV=test
        export POSTGRES_APPNAME=wpgx
        make test-container
        print_success "容器模式测试完成！"
        ;;
    2)
        print_section "运行直连模式测试"
        print_info "需要确保 PostgreSQL 正在运行 (localhost:5432)"
        read -p "PostgreSQL 是否已启动？(y/n): " pg_ready
        if [ "$pg_ready" != "y" ]; then
            print_info "正在启动 PostgreSQL 容器..."
            make docker-postgres-start
        fi
        print_info "运行测试..."
        make test-cmd
        print_success "直连模式测试完成！"
        if [ "$pg_ready" != "y" ]; then
            print_info "清理 PostgreSQL 容器..."
            make docker-postgres-stop
        fi
        ;;
    3)
        print_section "测试两种模式"
        
        # 容器模式
        print_info "1/2 - 容器模式"
        export WPGX_TEST_USE_CONTAINER=true
        export ENV=test
        export POSTGRES_APPNAME=wpgx
        make test-container
        print_success "容器模式 ✅"
        
        # 直连模式
        print_info "2/2 - 直连模式"
        unset WPGX_TEST_USE_CONTAINER
        print_info "启动 PostgreSQL..."
        make docker-postgres-start
        make test-cmd
        print_info "清理 PostgreSQL..."
        make docker-postgres-stop
        print_success "直连模式 ✅"
        
        print_success "两种模式都测试完成！"
        ;;
    4)
        print_section "编译检查"
        go build -v ./...
        print_success "编译成功！"
        ;;
    *)
        echo "❌ 无效选项"
        exit 1
        ;;
esac

echo ""
print_section "完成"
echo "测试模式说明："
echo "  • 容器模式: export WPGX_TEST_USE_CONTAINER=true"
echo "  • 直连模式: 不设置环境变量（默认）"
echo ""
echo "快速命令："
echo "  make test-container  # 容器模式"
echo "  make test            # 直连模式（需要手动启动 PG）"
echo ""

