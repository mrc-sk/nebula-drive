package host

import (
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// v2 宿主必须「自动兼容 v1」：示例插件 examples/plugins/referer-guard
// 在 init 里硬校验 HostProtocol == 1，看到 2 就直接拒绝启动。
// 宿主握手时按插件声明的协议版本降级，老插件一行代码都不用改。
func TestHandshakeProtocolKeepsV1PluginsWorking(t *testing.T) {
	cases := []struct {
		declared int
		want     int
	}{
		{1, 1}, // v1 老插件：宿主表现为 v1
		{2, 2}, // v2 插件：拿到当前协议
		{0, plugin.ProtocolVersion},
		{99, plugin.ProtocolVersion},
	}
	for _, c := range cases {
		if got := handshakeProtocol(c.declared); got != c.want {
			t.Errorf("handshakeProtocol(%d) = %d, want %d", c.declared, got, c.want)
		}
	}
}
