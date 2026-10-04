package main

// 测试用假插件：init 失败型。
// 报告 ok=false 并给出原因，宿主应拒绝加载并清理 registry。

import (
	"bufio"
	"encoding/json"
	"os"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		b, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"ok":    false,
				"error": "缺少必需配置项 apiKey",
			},
		})
		if err != nil {
			return
		}
		os.Stdout.Write(append(b, '\n'))
		return
	}
}
