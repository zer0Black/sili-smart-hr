// Package snowflake 封装雪花 ID 生成器，作为全系统默认主键策略。
//
// 主键 int64 由应用层在写入前生成，不依赖数据库自增，分布式部署天然不冲突，
// 且 ID 不可枚举，不暴露业务量。NodeID 在多实例部署时须唯一（0 到 1023），
// 单进程姿态用默认 1 即可。
//
// 初始化由 model.InitDB 在启动期完成（传入配置的 NodeID），未初始化时 NextID panic，
// 保证启动链路不落下 ID 生成器。
package snowflake

import (
	"fmt"

	sf "github.com/bwmarrin/snowflake"
)

var node *sf.Node

// Init 初始化雪花节点，nodeID 多实例部署时需唯一（0 到 1023）。
func Init(nodeID int64) error {
	n, err := sf.NewNode(nodeID)
	if err != nil {
		return fmt.Errorf("new snowflake node: %w", err)
	}
	node = n
	return nil
}

// NextID 生成下一个雪花 ID（int64）。未初始化时 panic，启动期必须先 Init。
func NextID() int64 {
	if node == nil {
		panic("snowflake node not initialized; call Init first")
	}
	return node.Generate().Int64()
}
