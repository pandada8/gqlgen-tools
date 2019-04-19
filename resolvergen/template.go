package resolvergen

import (
	"bytes"
	"fmt"
	"go/ast"
	"text/template"
)

var ResolverTypeTemplate = template.Must(template.New("resolverType").Parse(`
type {{.name}} struct{ *Resolver }
`))

func renderTemplate(tpl *template.Template, data interface{}) (ret *bytes.Buffer, err error) {
	ret = &bytes.Buffer{}
	err = tpl.Execute(ret, data)
	return
}

var ResolverFuncTemplate = template.Must(template.New("resolverFunc").Parse(`{{if not .IsRoot}}package resolver
import (
	"context"
)
{{end}}
func (r *{{.ResolverName}}) {{.MethodName}}({{range .Param}}{{.Name}} {{.Type}},{{end}}) ({{range .Result}}{{.Name}} {{.Type}},{{end}}) {
	{{if .IsRoot}}return &{{.ReturnType}}{r}{{else}}panic("not implemented"){{end}}
}
`))

type ResolverFuncData struct {
	ResolverName string
	MethodName   string
	field        *ast.Field
	rewriter     *ResolverCodeRewriter
}

func (r ResolverFuncData) IsRoot() bool {
	return r.ResolverName == "Resolver"
}

func (r ResolverFuncData) ReturnType() string {
	return lcFirst(r.MethodName) + "Resolver"
}

func (r ResolverFuncData) Param() (ret []NameTypePair) {
	funcType := r.field.Type.(*ast.FuncType)
	if funcType.Params == nil {
		return
	}
	for i, param := range funcType.Params.List {
		pair := NameTypePair{}
		if len(param.Names) > 0 {
			pair.Name = param.Names[0].Name
		} else {
			pair.Name = fmt.Sprintf("parm%d", i)
		}
		pair.Type = r.rewriter.resolverType("gql", param.Type)
		ret = append(ret, pair)
	}
	return
}

func (r ResolverFuncData) Result() (ret []NameTypePair) {
	funcType := r.field.Type.(*ast.FuncType)
	if funcType.Results == nil {
		return
	}
	for i, param := range funcType.Results.List {
		pair := NameTypePair{}
		pair.Type = r.rewriter.resolverType("gql", param.Type)
		if len(param.Names) > 0 {
			pair.Name = param.Names[0].Name
		} else if pair.Type == "error" {
			pair.Name = "err"
		} else if i == 0 {
			pair.Name = "ret"
		} else {
			pair.Name = fmt.Sprintf("ret%d", i)
		}
		ret = append(ret, pair)
	}
	return
}

type NameTypePair struct {
	Type string
	Name string
}
