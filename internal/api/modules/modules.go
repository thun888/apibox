// Package modules 模块汇入点
// 每新增一个子模块，在此文件追加一行匿名导入即可
package modules

import (
	_ "github.com/thun888/apibox/internal/api/modules/order"
	_ "github.com/thun888/apibox/internal/api/modules/user"
)
