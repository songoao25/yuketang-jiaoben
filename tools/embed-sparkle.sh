#!/usr/bin/env bash
# 把 Sparkle.framework 嵌入 .app、写 Info.plist 更新配置，并用稳定证书重新签名。
#
# 用法：
#   tools/embed-sparkle.sh <app-path> <sparkle-dir> [feed-url]
# 环境变量：
#   IDENTITY  签名身份（默认 Apple Development: crabapplebandit@icloud.com (MG8SD67SC5)）
#   SU_PUBLIC_KEY  覆盖默认 Sparkle EdDSA 公钥
set -euo pipefail

APP="${1:?用法: embed-sparkle.sh <app> <sparkle-dir> [feed-url]}"
SPARKLE_DIR="${2:?缺少 Sparkle 目录}"
FEED_URL="${3:-https://github.com/songoao25/yuketang-jiaoben/releases/latest/download/appcast.xml}"
IDENTITY="${IDENTITY:-Apple Development: crabapplebandit@icloud.com (MG8SD67SC5)}"
PUBLIC_KEY="${SU_PUBLIC_KEY:-A1YuAtvtrNrwWc8P6bb9uZ7UCHnl8upSmkNPnBvy4Zk=}"

[ -d "$APP" ] || { echo "✖ App 不存在：$APP" >&2; exit 1; }
[ -d "$SPARKLE_DIR/Sparkle.framework" ] || { echo "✖ 找不到 $SPARKLE_DIR/Sparkle.framework" >&2; exit 1; }

FRAMEWORKS="$APP/Contents/Frameworks"
PLIST="$APP/Contents/Info.plist"
mkdir -p "$FRAMEWORKS"
rm -rf "$FRAMEWORKS/Sparkle.framework"
/usr/bin/ditto "$SPARKLE_DIR/Sparkle.framework" "$FRAMEWORKS/Sparkle.framework"

/usr/libexec/PlistBuddy -c "Delete :SUFeedURL" "$PLIST" >/dev/null 2>&1 || true
/usr/libexec/PlistBuddy -c "Add :SUFeedURL string $FEED_URL" "$PLIST"
/usr/libexec/PlistBuddy -c "Delete :SUPublicEDKey" "$PLIST" >/dev/null 2>&1 || true
/usr/libexec/PlistBuddy -c "Add :SUPublicEDKey string $PUBLIC_KEY" "$PLIST"
/usr/libexec/PlistBuddy -c "Delete :SUEnableAutomaticChecks" "$PLIST" >/dev/null 2>&1 || true
/usr/libexec/PlistBuddy -c "Add :SUEnableAutomaticChecks bool true" "$PLIST"
/usr/libexec/PlistBuddy -c "Delete :SUScheduledCheckInterval" "$PLIST" >/dev/null 2>&1 || true
/usr/libexec/PlistBuddy -c "Add :SUScheduledCheckInterval integer 86400" "$PLIST"

echo "› 签署 Sparkle.framework"
/usr/bin/codesign --force --deep --sign "$IDENTITY" --timestamp=none "$FRAMEWORKS/Sparkle.framework"
echo "› 签署 App：$IDENTITY"
/usr/bin/codesign --force --deep --sign "$IDENTITY" --timestamp=none "$APP"
/usr/bin/codesign --verify --deep --strict "$APP"
echo "✔ Sparkle 已嵌入并签名：$APP"
