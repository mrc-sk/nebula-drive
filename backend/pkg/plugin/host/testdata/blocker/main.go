package main

// 测试用假插件：拦截型。
// fileName 含 "blockme" 就返回 error "blocked by test plugin"。

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
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
			}, 0, "")
		case "fire":
			var p struct {
				Ctx map[string]any `json:"ctx"`
			}
			_ = json.Unmarshal(r.Params, &p)
			name, _ := p.Ctx["fileName"].(string)
			if strings.Contains(name, "blockme") {
				emit(r.ID, map[string]any{
					"error": "blocked by test plugin",
				}, 0, "")
			} else {
				emit(r.ID, map[string]any{
					"ctxPatch": map[string]any{"allowed": true},
				}, 0, "")
			}
		case "shutdown":
			emit(r.ID, map[string]any{"bye": true}, 0, "")
			return
		default:
			emit(r.ID, nil, -32601, "unsupported")
		}
	}
}

func emit(id int64, result any, code int, msg string) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if code != 0 {
		m["error"] = map[string]any{"code": code, "message": msg}
	} else {
		m["result"] = result
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	os.Stdout.Write(append(b, '\n'))
}
