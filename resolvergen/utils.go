package resolvergen

import (
	"go/ast"
	"go/token"
	"log"
	"reflect"
	"strings"
	"unicode"

	"github.com/davecgh/go-spew/spew"
)

func lcFirst(s string) string {
	if s == "" {
		return ""
	}

	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func snake(i string) (ret string) {
	runes := []rune(i)
	prev := false
	current := false
	str := strings.Builder{}
	for _, r := range runes {
		current = unicode.IsLower(r)
		if prev && !current {
			str.WriteString("_")
		}
		str.WriteRune(unicode.ToLower(r))
		prev = current
	}
	return str.String()
}

func snakify(recv, method string) (ret string) {
	if recv == "Resolver" {
		return "base.go"
	}
	return strings.ToLower(strings.TrimSuffix(recv, "Resolver")) + "_" + snake(method) + ".go"
}

func hasPaniced(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return true
	}
	first := body.List[0]
	if stmt, ok := first.(*ast.ExprStmt); ok {
		if call, ok := stmt.X.(*ast.CallExpr); ok {
			if fun, ok := call.Fun.(*ast.Ident); ok {
				if fun.Name == "panic" {
					return true
				}
			}
		}
	}
	return false
}

var panicHere = &ast.ExprStmt{
	X: &ast.CallExpr{
		Fun: ast.NewIdent("panic"),
		Args: []ast.Expr{
			&ast.BasicLit{
				Kind:  token.STRING,
				Value: `"FIXME: method signature updated, please check"`,
			},
		},
	},
}

var basicTypes = map[string]struct{}{
	"string":  struct{}{},
	"int":     struct{}{},
	"int8":    struct{}{},
	"int16":   struct{}{},
	"int32":   struct{}{},
	"int64":   struct{}{},
	"uint":    struct{}{},
	"uint8":   struct{}{},
	"uint16":  struct{}{},
	"uint32":  struct{}{},
	"uint64":  struct{}{},
	"float":   struct{}{},
	"float64": struct{}{},
	"error":   struct{}{},
	"bool":    struct{}{},
}

func isBasicType(t string) (ret bool) {
	_, ret = basicTypes[t]
	return
}

func cleanPos(node ast.Node) {
	switch nodeT := node.(type) {
	case *ast.Field:
		for _, name := range nodeT.Names {
			cleanPos(name)
		}
		cleanPos(nodeT.Type)
	case *ast.SelectorExpr:
		cleanPos(nodeT.X)
		cleanPos(nodeT.Sel)
	case *ast.Ident:
		nodeT.NamePos = 0
	case *ast.StarExpr:
		nodeT.Star = 0
		cleanPos(nodeT.X)
	default:
		spew.Dump(node)
		log.Println(node.Pos())
		if node == nil {
			return
		}
		vo := reflect.ValueOf(node)
		if vo.IsNil() {
			return
		}
		v := vo.Elem()
		for j := 0; j < v.NumField(); j++ {
			f := v.Field(j)
			if f.Type().Name() == "Pos" {
				f.SetInt(0)
			}
		}
		spew.Dump(node)
	}

}
