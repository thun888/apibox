// Package modules 模块汇入点
// 每新增一个子模块，在此文件追加一行匿名导入即可
package modules

import (
	_ "github.com/thun888/apibox/internal/api/modules/biliinfo"
	_ "github.com/thun888/apibox/internal/api/modules/genlineanimation"
	_ "github.com/thun888/apibox/internal/api/modules/qqmailhead"
	_ "github.com/thun888/apibox/internal/api/modules/starhistory"
	_ "github.com/thun888/apibox/internal/api/modules/starvote"
)
