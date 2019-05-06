package resolvergen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"io/ioutil"
	"log"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/davecgh/go-spew/spew"
	"golang.org/x/tools/go/packages"
)

type ResolverCodeRewriter struct {
	gql      *RewriterPackage
	resolver *RewriterPackage
	pending  map[string]*bytes.Buffer
	changed  map[string]bool
}

func NewResolverRewriter(gql, resolver string) *ResolverCodeRewriter {
	return &ResolverCodeRewriter{
		gql:      NewRewriterPackage(gql),
		resolver: NewRewriterPackage(resolver),
		pending:  map[string]*bytes.Buffer{},
		changed:  map[string]bool{},
	}
}

type RewriterPackage struct {
	path           string
	pkg            *packages.Package
	funcDecls      map[string]*ast.FuncDecl
	typeSpecs      map[string]*ast.TypeSpec
	interfaceTypes map[string]*ast.InterfaceType
	fileMap        map[string]string
}

func (r *RewriterPackage) GetSyntaxByFilename(filename string) *ast.File {
	for i, file := range r.pkg.CompiledGoFiles {
		if path.Base(file) == filename {
			return r.pkg.Syntax[i]
		}
	}
	return nil
}

func NewRewriterPackage(packagePath string) *RewriterPackage {
	pkg, err := packages.Load(&packages.Config{
		Mode: packages.LoadAllSyntax,
	}, packagePath)
	if err != nil {
		log.Println(err)
		return nil
	}

	ret := &RewriterPackage{
		path:           path.Dir(pkg[0].CompiledGoFiles[0]),
		pkg:            pkg[0],
		funcDecls:      map[string]*ast.FuncDecl{},
		typeSpecs:      map[string]*ast.TypeSpec{},
		interfaceTypes: map[string]*ast.InterfaceType{},
		fileMap:        map[string]string{},
	}
	for i, file := range ret.pkg.Syntax {
		for _, decl := range file.Decls {
			ast.Inspect(decl, func(node ast.Node) bool {
				switch nodeT := node.(type) {
				case *ast.FuncDecl:
					name := nodeT.Name.Name
					if nodeT.Recv != nil {
						switch typeT := nodeT.Recv.List[0].Type.(type) {
						case *ast.StarExpr:
							name = typeT.X.(*ast.Ident).Name + "." + name
						case *ast.Ident:
							name = typeT.Name + "." + name
						}
					}
					ret.fileMap[name] = ret.pkg.CompiledGoFiles[i]
					ret.funcDecls[name] = nodeT
				case *ast.TypeSpec:
					ret.typeSpecs[nodeT.Name.Name] = nodeT
					if interfaceType, ok := nodeT.Type.(*ast.InterfaceType); ok {
						ret.interfaceTypes[nodeT.Name.Name] = interfaceType
					}
				}
				return true
			})
		}
	}
	return ret
}

func (r *ResolverCodeRewriter) Rewrite() {
	r.RewriteInterface()
}

func (r *ResolverCodeRewriter) addToPending(name string, buf *bytes.Buffer) {
	if !strings.Contains(name, "/") {
		name = path.Join(r.resolver.path, name)
	}
	if _, ok := r.pending[name]; ok {
		r.pending[name].Write(buf.Bytes())
	} else {
		r.pending[name] = buf
	}
	r.changed[name] = true
	return
}

func (r *ResolverCodeRewriter) resolverType(packageName string, node ast.Node) (ret string) {
	switch nodeT := node.(type) {
	case *ast.Ident:
		name := nodeT.Name
		if isBasicType(name) {
			return name
		}
		return packageName + "." + name
	case *ast.StarExpr:
		return "*" + r.resolverType(packageName, nodeT.X)
	case *ast.SelectorExpr:
		return nodeT.X.(*ast.Ident).Name + "." + nodeT.Sel.Name
	case *ast.Field:
		return r.resolverType(packageName, nodeT.Type)
	case *ast.ChanType:
		modifier := "chan "
		if nodeT.Dir == ast.RECV {
			modifier = "<-" + modifier
		} else if nodeT.Dir == ast.SEND {
			modifier = "->" + modifier
		}
		return modifier + r.resolverType(packageName, nodeT.Value)
	case *ast.ArrayType:
		return "[]" + r.resolverType(packageName, nodeT.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", r.resolverType(packageName, nodeT.Key), r.resolverType(packageName, nodeT.Value))
	case *ast.InterfaceType:
		if nodeT.Methods.List == nil {
			return "interface{}"
		}
		log.Println("unkown interface method list")
		spew.Dump(nodeT.Methods)
	default:
		log.Println("unkown type when resolverType")
		spew.Dump(node)
	}
	return
}

func (r *ResolverCodeRewriter) rewriteField(field *ast.Field) *ast.Field {
	// FIXME: remove all pos information
	result := astutil.Apply(field, func(c *astutil.Cursor) bool {
		switch nodeT := c.Node().(type) {
		case *ast.CommentGroup:
			c.Delete()
		case *ast.Field:
			switch nodeTT := nodeT.Type.(type) {
			case *ast.Ident:
				if isBasicType(nodeTT.Name) {
					break
				}
				c.Replace(
					&ast.Field{
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent("gql"),
							Sel: ast.NewIdent(nodeTT.Name),
						},
					},
				)
			default:
			}
		}
		return true
	}, nil)
	cleanPos(result)
	return result.(*ast.Field)
}

func (r *ResolverCodeRewriter) isSameFieldType(resolverType, gqlType *ast.Field) bool {
	return r.resolverType("gql", gqlType) == r.resolverType("resolver", resolverType)
}

func (r *ResolverCodeRewriter) checkOrUpdateMethod(method *ast.FuncDecl, field *ast.Field) bool {
	var (
		dirtyParams  bool
		dirtyResults bool
	)
	methodType := method.Type
	fieldType := field.Type.(*ast.FuncType)

	if (methodType.Params == nil) != (fieldType.Params == nil) {
		dirtyParams = true
	} else {
		if methodType.Params != nil && fieldType.Params != nil {
			if len(methodType.Params.List) == len(fieldType.Params.List) {
				for i, methodParam := range methodType.Params.List {
					if !r.isSameFieldType(methodParam, fieldType.Params.List[i]) {
						dirtyParams = true
						break
					}
				}
			} else {
				dirtyParams = true
			}
		}
	}

	if dirtyParams {
		if methodType.Params == nil {
			methodType.Params = &ast.FieldList{}
		}
		methodType.Params.List = []*ast.Field{}
		for _, originParam := range fieldType.Params.List {
			methodType.Params.List = append(methodType.Params.List, r.rewriteField(originParam))
		}
	}

	if (methodType.Results == nil) != (fieldType.Results == nil) {
		dirtyResults = true
	} else {
		if methodType.Results != nil && fieldType.Results != nil {
			if len(methodType.Results.List) == len(fieldType.Results.List) {
				for i, methodParam := range methodType.Results.List {
					if !r.isSameFieldType(methodParam, fieldType.Results.List[i]) {
						dirtyResults = true
						break
					}
				}
			} else {
				dirtyResults = true
			}
		}
	}

	if dirtyResults {
		if methodType.Results == nil {
			methodType.Results = &ast.FieldList{}
		}
		methodType.Results.List = []*ast.Field{}
		for _, originParam := range fieldType.Results.List {
			methodType.Results.List = append(methodType.Results.List, r.rewriteField(originParam))
		}
	}

	if dirtyParams || dirtyResults {
		// try insert panic Here
		if !hasPaniced(method.Body) {
			method.Body.List = append([]ast.Stmt{panicHere}, method.Body.List...)
		}
	}
	return dirtyParams || dirtyResults
}

func (r *ResolverCodeRewriter) RewriteInterface() {
	for name, interfaceType := range r.gql.interfaceTypes {
		resolverName := convertName(name)
		// check if resolver has same name struct
		if _, ok := r.resolver.typeSpecs[resolverName]; ok {
			// check submethods
			for _, field := range interfaceType.Methods.List {
				submethodName := resolverName + "." + field.Names[0].Name
				if submethodImpl, ok := r.resolver.funcDecls[submethodName]; ok {
					if r.checkOrUpdateMethod(submethodImpl, field) {
						log.Println(r.resolver.fileMap[submethodName])
						r.changed[r.resolver.fileMap[submethodName]] = true
					}
				} else {
					buf, err := renderTemplate(ResolverFuncTemplate, ResolverFuncData{
						ResolverName: resolverName,
						MethodName:   field.Names[0].Name,
						field:        field,
						rewriter:     r,
					})
					if err != nil {
						log.Println(err)
						continue
					}
					log.Println(resolverName, field.Names[0].Name)
					r.addToPending(snakify(resolverName, field.Names[0].Name), buf)
				}
			}
			// FIXME: check if some exported method is ending with Resolver and not used in interface
			// transfer them into comment group
		} else {
			// FIXME: support generating from
			// missing xxxxResolver
			log.Println(resolverName)
			buf, err := renderTemplate(ResolverTypeTemplate, map[string]string{"name": resolverName})
			if err != nil {
				log.Println(err)
				continue
			}
			r.addToPending("base.go", buf)
		}
	}
	// FIXME: do some comment transfermation for extra Resolver types
	// FIXME: codegen more with complexity
	return
}
func (r *ResolverCodeRewriter) Write() {
	files := map[string][]byte{}
	// collecting ast
	for i, filename := range r.resolver.pkg.CompiledGoFiles {
		var buf bytes.Buffer
		err := printer.Fprint(&buf, r.resolver.pkg.Fset, r.resolver.pkg.Syntax[i])
		if err != nil {
			log.Println(err)
			continue
		}
		files[filename] = buf.Bytes()
	}

	// merge with pending
	for filename, buf := range r.pending {
		if _, ok := files[filename]; ok {
			files[filename] = append(files[filename], buf.Bytes()...)
		} else {
			files[filename] = buf.Bytes()
		}
	}
	// output with goreturns
	// FIXME: test if goreturns existed

	for filename, file := range files {
		if _, ok := r.changed[filename]; !ok {
			continue
		}
		var buf bytes.Buffer
		goreturns := exec.Command("goreturns", "-srcdir", r.resolver.path)
		goreturns.Stdin = bytes.NewBuffer(file)
		goreturns.Stdout = &buf
		err := goreturns.Run()
		var output []byte
		if err != nil {
			log.Println(err)
			output = file
		} else {
			output = buf.Bytes()
		}
		err = ioutil.WriteFile(filename, output, 0644)
		if err != nil {
			log.Println(err)
		}
		log.Println("updated", filename)
	}
	return
}

var rightResolver = regexp.MustCompile("Resolver$")

func convertName(name string) string {
	if name == "ResolverRoot" {
		return "Resolver"
	}
	return lcFirst(rightResolver.ReplaceAllString(name, "")) + "Resolver"
}
