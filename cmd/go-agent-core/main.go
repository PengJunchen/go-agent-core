// Package main 是 go-agent-core 的 CLI 入口（M0 骨架）。
//
// 当前仅打印版本信息与已注册 Provider，验证模块可构建。
// 后续里程碑逐步接入 LoopAgent、配置加载、HTTP/A2A/ACP 服务器。
package main

import (
	"fmt"
	"os"

	"github.com/pengjunchen/go-agent-core/llm/registry"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("go-agent-core v0.0.1-dev (M0)")
		return
	}
	fmt.Println("go-agent-core — interface-driven agent framework")
	fmt.Println()
	fmt.Println("Registered providers:")
	for _, name := range registry.DefaultRegistry.ListProviders() {
		fmt.Printf(" - %s\n", name)
	}
	if len(registry.DefaultRegistry.ListProviders()) == 0 {
		fmt.Println(" (none yet — providers self-register via init() in M1)")
	}
}
