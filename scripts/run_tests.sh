#!/bin/bash

set -euo pipefail

# 完整离线测试入口

echo "🚀 开始运行项目离线测试..."

# 设置Go环境变量
export GO111MODULE=on

# 默认测试必须完全离线，真实通知只能通过 integration build tag 显式运行。
if go test -list . ./... | grep '^TestRealNotice$' >/dev/null; then
    echo 'TestRealNotice must not be in the default test set' >&2
    exit 1
fi

# 运行所有通知相关的测试
echo "📋 运行通知管理器测试..."
go test -count=1 -run "Test.*Notification" ./...

echo "📋 运行通知器实现测试..."
go test -count=1 -run "Test.*Notifier" ./...

echo "📋 运行配置相关测试..."
go test -count=1 -run "Test.*Config" ./...

echo "📋 运行全部测试..."
go test -count=1 ./...

echo "📊 运行覆盖率测试..."
go test -coverprofile=coverage.out ./...

echo "📈 生成覆盖率报告..."
go tool cover -html=coverage.out -o coverage.html

echo "⚡ 运行基准测试..."
go test -bench=. -run='^$' ./...

echo "✅ 所有测试完成！"
echo "📊 覆盖率报告已生成：coverage.html"

# 显示测试覆盖率摘要
echo "📈 测试覆盖率摘要："
go tool cover -func=coverage.out | tail -1
