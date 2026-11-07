package plugin

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

// register.go 是 v2 插件的**纯声明文件**。
//
// 关键：它虽然叫 .go，但宿主**不编译、不执行、不要求服务器装 Go**。
// 宿主只用标准库 go/parser 读它的语法树，把 `var Spec = ...{...}` 里的
// 字面量取出来当一个声明清单用。因此文件里引用的类型可以根本不存在，
// 也不需要有合法的 import —— 它就是一份「长得像 Go 的配置文件」。
//
// 这么设计是为了让插件作者写起来顺手（IDE 有高亮、能写注释），
// 同时不破坏「单二进制、什么都不用装」的部署承诺。

// GoDeclFileName 注册声明文件名。
const GoDeclFileName = "register.go"

// GoDecl register.go 里能识别的声明字段。
type GoDecl struct {
	Name            string
	Version         string
	Title           string
	Author          string
	Description     string
	ProtocolVersion int
	Hooks           []string
	Provides        []string
	Install         []string
	Runtime         string
	Home            string
}

// SpecVarNames 宿主识别的声明变量名（按优先级）。
var SpecVarNames = []string{"Spec", "Plugin", "PluginSpec", "Declaration"}

// ParseGoDecl 解析一份 register.go。
func ParseGoDecl(path string) (*GoDecl, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", GoDeclFileName, err)
	}
	if len(src) > 256<<10 {
		return nil, fmt.Errorf("%s 超过 256KB 上限", GoDeclFileName)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%s 语法解析失败（注意：宿主只解析语法，不编译）: %w", GoDeclFileName, err)
	}
	if f == nil {
		return nil, errors.New("register.go 解析结果为空")
	}

	var cl *ast.CompositeLit
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			if !matchSpecName(vs.Names) {
				continue
			}
			if lit, ok := vs.Values[0].(*ast.CompositeLit); ok {
				cl = lit
				break
			}
			// 也接受 map[string]any 形式
			if lit, ok := vs.Values[0].(*ast.CompositeLit); ok && lit.Type != nil {
				cl = lit
				break
			}
		}
		if cl != nil {
			break
		}
	}
	if cl == nil {
		return nil, fmt.Errorf("%s 中找不到声明变量（应为 var Spec = ...{...}，可用变量名：%s）",
			GoDeclFileName, strings.Join(SpecVarNames, "/"))
	}

	fields := parseComposite(cl)
	d := &GoDecl{
		Name:            strField(fields, "Name", "name"),
		Version:         strField(fields, "Version", "version"),
		Title:           strField(fields, "Title", "title"),
		Author:          strField(fields, "Author", "author"),
		Description:     strField(fields, "Description", "description"),
		Runtime:         strField(fields, "Runtime", "runtime"),
		Home:            strField(fields, "Home", "home"),
		ProtocolVersion: intField(fields, "ProtocolVersion", "protocolVersion"),
		Hooks:           strSliceField(fields, "Hooks", "hooks"),
		Provides:        strSliceField(fields, "Provides", "provides"),
		Install:         strSliceField(fields, "Install", "install"),
	}
	if d.Name == "" {
		return nil, fmt.Errorf("%s 的 Spec 缺少 Name 字段", GoDeclFileName)
	}
	return d, nil
}

func matchSpecName(names []*ast.Ident) bool {
	for _, n := range names {
		for _, want := range SpecVarNames {
			if n.Name == want {
				return true
			}
		}
	}
	return false
}

// parseComposite 把 CompositeLit 的 KeyValueExpr 抽成 map[string]any。
// 只认字符串/整数字面量与字符串切片/嵌套 map —— 声明文件不需要表达式求值。
func parseComposite(lit *ast.CompositeLit) map[string]any {
	out := map[string]any{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key := ""
		switch k := kv.Key.(type) {
		case *ast.Ident:
			key = k.Name
		case *ast.BasicLit:
			if k.Kind == token.STRING {
				key, _ = strconv.Unquote(k.Value)
			}
		}
		if key == "" {
			continue
		}
		out[key] = parseValue(kv.Value)
	}
	return out
}

func parseValue(e ast.Expr) any {
	switch v := e.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			s, err := strconv.Unquote(v.Value)
			if err == nil {
				return s
			}
			return v.Value
		case token.INT:
			n, err := strconv.Atoi(v.Value)
			if err == nil {
				return n
			}
			return v.Value
		default:
			return v.Value
		}
	case *ast.CompositeLit:
		// []string{...} → []any；map[...] 或 struct → map[string]any
		isKV := false
		for _, el := range v.Elts {
			if _, ok := el.(*ast.KeyValueExpr); ok {
				isKV = true
				break
			}
		}
		if isKV {
			return parseComposite(v)
		}
		arr := make([]any, 0, len(v.Elts))
		for _, el := range v.Elts {
			arr = append(arr, parseValue(el))
		}
		return arr
	case *ast.Ident:
		return v.Name
	default:
		return nil
	}
}

func lookup(fields map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := fields[k]; ok {
			return v
		}
	}
	// 大小写不敏感兜底
	for k, v := range fields {
		for _, want := range keys {
			if strings.EqualFold(k, want) {
				return v
			}
		}
	}
	return nil
}

func strField(fields map[string]any, keys ...string) string {
	if v, ok := lookup(fields, keys...).(string); ok {
		return v
	}
	return ""
}

func intField(fields map[string]any, keys ...string) int {
	switch v := lookup(fields, keys...).(type) {
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func strSliceField(fields map[string]any, keys ...string) []string {
	v := lookup(fields, keys...)
	arr, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
