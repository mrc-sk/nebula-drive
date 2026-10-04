package main

// 测试用假插件：崩溃型。
// fire 时不响应直接退出，用于验证"插件崩溃不影响宿主"。

import (
	"bufio"
	"encoding/json"
	"os"
)

type req struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 65536), 8<<20)
	for sc.Scan() {
		var r req
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		switch r.Method {
		case "init":
			emit(r.ID, map[string]any{
				"ok": true, "hooks": []string{"onAntiLeech"},
			})
		case "fire":
			// 不响应，直接死
			os.Exit(3)
		case "shutdown":
			emit(r.ID, map[string]any{"bye": true})
			return
		default:
			emit(r.ID, map[string]any{"pong": true})
		}
	}
}

func emit(id int64, result any) {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "result": result,
	})
	if err != nil {
		return
	}
	os.Stdout.Write(append(b, '\n'))
}
