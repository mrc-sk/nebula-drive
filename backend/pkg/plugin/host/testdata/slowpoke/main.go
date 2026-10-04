package main

// 测试用假插件：超时型。
// fire 时睡 30 秒，触发宿主侧 FireTimeout。

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
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
			time.Sleep(30 * time.Second)
		case "shutdown":
			emit(r.ID, map[string]any{"bye": true})
			return
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
