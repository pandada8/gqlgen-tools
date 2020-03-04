package resolvergen

import (
	"github.com/99designs/gqlgen/codegen"
	"github.com/99designs/gqlgen/plugin"
)

func New(resolverPkg, gqlPkg string) plugin.Plugin {
	return &Plugin{ResolverPkg: resolverPkg, GqlPkg: gqlPkg}
}

type Plugin struct {
	ResolverPkg string
	GqlPkg      string
}

var _ plugin.CodeGenerator = &Plugin{}

func (m *Plugin) Name() string {
	return "resovlergen"
}
func (m *Plugin) GenerateCode(data *codegen.Data) error {
	rewriter := NewResolverRewriter(m.GqlPkg, m.ResolverPkg)
	rewriter.Rewrite()
	rewriter.Write()
	return nil
}
