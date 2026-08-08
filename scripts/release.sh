#!/usr/bin/env bash
#
# 统一版本发布脚本：为所有 Go 模块打同一版本号的 tag。
#
# 用法:
#   ./scripts/release.sh v0.6.0          # dry-run（仅打印，不执行）
#   ./scripts/release.sh v0.6.0 --push   # 创建 tag 并推送到 origin
#
# 原理:
#   遍历 repo 中所有 go.mod，按目录路径生成 tag：
#     - 根目录 go.mod → v0.6.0
#     - contrib/llm/go.mod → contrib/llm/v0.6.0
#     - tools/go.mod → tools/v0.6.0
#
#   这保证所有模块版本号一致，用户只需记一个版本号。

set -euo pipefail

VERSION="${1:-}"
PUSH="${2:-}"

if [[ -z "$VERSION" ]]; then
    echo "用法: $0 <version> [--push]"
    echo "示例: $0 v0.6.0 --push"
    exit 1
fi

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "错误: 版本号格式应为 vX.Y.Z (如 v0.6.0)"
    exit 1
fi

cd "$(git rev-parse --show-toplevel)"

TAGS=()

while IFS= read -r modfile; do
    dir=$(dirname "$modfile")
    if [[ "$dir" == "." ]]; then
        tag="$VERSION"
    else
        tag="${dir}/${VERSION}"
    fi
    TAGS+=("$tag")
done < <(find . -name "go.mod" -not -path "*/vendor/*" | sed 's|^\./||' | sort)

echo "=== 版本: $VERSION ==="
echo "=== 共 ${#TAGS[@]} 个模块 ==="
echo ""

EXISTING=()
NEW=()
for tag in "${TAGS[@]}"; do
    if git rev-parse "$tag" >/dev/null 2>&1; then
        EXISTING+=("$tag")
    else
        NEW+=("$tag")
    fi
done

if [[ ${#EXISTING[@]} -gt 0 ]]; then
    echo "⚠ 已存在的 tag（跳过）:"
    for tag in "${EXISTING[@]}"; do
        echo "  $tag"
    done
    echo ""
fi

if [[ ${#NEW[@]} -eq 0 ]]; then
    echo "所有 tag 已存在，无需操作。"
    exit 0
fi

echo "将创建的 tag:"
for tag in "${NEW[@]}"; do
    echo "  $tag"
done
echo ""

if [[ "$PUSH" != "--push" ]]; then
    echo "(dry-run 模式，加 --push 参数执行)"
    exit 0
fi

for tag in "${NEW[@]}"; do
    git tag "$tag"
    echo "✓ 创建 $tag"
done

echo ""
echo "推送到 origin..."
git push origin "${NEW[@]}"
echo ""
echo "=== 完成: ${#NEW[@]} 个 tag 已推送 ==="
