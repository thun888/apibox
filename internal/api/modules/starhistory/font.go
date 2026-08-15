package starhistory

import (
	_ "embed"
	"strings"
)

// xkcd 手写字体（woff, base64），来源于 star-history 的
// shared/packages/utils/fontData.ts，渲染时内联到 @font-face 中。
//
//go:embed xkcd_font.b64
var xkcdFontB64 string

var xkcdFontDataURL = "data:application/font-woff;charset=utf-8;base64," + strings.TrimSpace(xkcdFontB64)
