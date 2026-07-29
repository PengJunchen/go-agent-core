// Package verify 的 CLI 入口：扫描项目并报告违规。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/pengjunchen/go-agent-core/verify"
)

func main() {
	dir := flag.String("dir", ".", "要扫描的目录")
	flag.Parse()

	scanner := verify.NewScanner()
	violations, err := scanner.ScanDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
		os.Exit(2)
	}

	fmt.Println(verify.FormatViolations(violations))
	if len(violations) > 0 {
		os.Exit(1)
	}
}
