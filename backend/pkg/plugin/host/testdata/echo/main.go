package main

// 测试用假插件：回显型。
// 把 ctx 的每个键加 echo_ 前缀写回，实现 ctxPatch 往返验证。

import (
	"bufio"
	"encoding/json"
	"os"
)

type req struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type resp struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      int64   `json:"id"`
	Result  any     `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
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
			write(resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{
				"ok": true, "name": "echo", "version": "1.0.0",
				"hooks": []string{"onAntiLeech"},
			}})
		case "ping":
			write(resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{"pong": true}})
		case "shutdown":
			write(resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{"bye": true}})
			return
		case "fire":
			var p struct {
				Hook string         `json:"hook"`
				Ctx  map[string]any `json:"ctx"`
			}
			_ = json.Unmarshal(r.Params, &p)
			patch := map[string]any{"touched": true}
			for k, v := range p.Ctx {
				patch["echo_"+k] = v
			}
			write(resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{"ctxPatch": patch}})
		default:
			write(resp{JSONRPC: "2.0", ID: r.ID, Error: &rpcErr{Code: -32601, Message: "unsupported " + r.Method}})
		}
	}
}

func write(r resp) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	os.Stdout.Write(append(b, '\n'))
}
