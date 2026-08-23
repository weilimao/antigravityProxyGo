//go:build !bindings

package main

// shouldCheckSingleInstance 返回 false,与原初始实现一致。
// 用户反馈"已习惯多实例并存"(虽然可能造成端口冲突,但保持原行为)。
func shouldCheckSingleInstance() bool {
	return false
}
