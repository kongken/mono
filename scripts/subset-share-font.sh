#!/usr/bin/env bash
# Rebuilds the font subset embedded for the share preview image
# (internal/tripmap/share/assets/NotoSansSC-*.subset.otf).
#
# Needs Python with fontTools (pip install fonttools) and network access to the
# official Noto CJK repository. Keep CHARS in sync with the fixed texts in
# internal/tripmap/share/og.go; TestFontCoversEveryRenderedCharacter fails when a glyph is missing.
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CHARS='山海足迹 / SHANHAI TRAVEL 我的旅行足迹 世界很大，慢慢留下足迹。 已探索省份 全国打卡城市 旅行次数 南海诸岛 记录于 打卡率 · 省级地图 仅公开聚合统计'
python3 - "$work/chars.txt" "$CHARS" <<'PY'
import sys
text = sys.argv[2]
chars = sorted(set(text) | {chr(c) for c in range(0x20, 0x7f)})
open(sys.argv[1], "w", encoding="utf8").write("".join(chars))
PY
for weight in Regular Bold; do
  curl -fsSL -o "$work/$weight.otf" "https://github.com/notofonts/noto-cjk/raw/main/Sans/SubsetOTF/SC/NotoSansSC-$weight.otf"
  python3 -m fontTools.subset "$work/$weight.otf" --text-file="$work/chars.txt" \
    --output-file="internal/tripmap/share/assets/NotoSansSC-$weight.subset.otf" \
    --layout-features='' --no-hinting --desubroutinize --name-IDs='*' --notdef-outline
done
